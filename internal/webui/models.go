package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mineagent/internal/config"
)

const modelCacheTTL = 10 * time.Minute
const modelProbeTTL = 20 * time.Minute

// ModelOption describes provider-advertised metadata, not a guarantee of quota/access.
type ModelOption struct {
	ID                string `json:"id"`
	Label             string `json:"label"`
	Provider          string `json:"provider"`
	ProviderLabel     string `json:"providerLabel"`
	Free              bool   `json:"free"`
	NeedsKey          bool   `json:"needsKey,omitempty"`
	Unavailable       bool   `json:"unavailable,omitempty"`
	Verified          bool   `json:"verified,omitempty"`
	Created           int64  `json:"created,omitempty"`
	ContextLength     int64  `json:"contextLength,omitempty"`
	UnavailableReason string `json:"unavailableReason,omitempty"`
	Stale             bool   `json:"stale,omitempty"`
}

// Contributor is a billing/program designation, not a free model.
func isFreeModelID(id string) bool {
	id = strings.ToLower(id)
	return strings.HasSuffix(id, "-free") || strings.HasSuffix(id, ":free") || id == "openrouter/free"
}

func zeroPrice(v string) bool {
	n, ok := new(big.Rat).SetString(strings.TrimSpace(v))
	return ok && n.Sign() == 0
}

// Pricing, when provided, overrides the naming fallback. Missing prices are unknown.
func freePricing(pricing map[string]string, id string) bool {
	if pricing == nil {
		return isFreeModelID(id)
	}
	if !zeroPrice(pricing["prompt"]) || !zeroPrice(pricing["completion"]) {
		return false
	}
	for _, field := range []string{"request", "image", "input_cache_read", "input_cache_write", "web_search", "internal_reasoning"} {
		if value, ok := pricing[field]; ok && !zeroPrice(value) {
			return false
		}
	}
	return true
}

func fetchModelOptions(ctx context.Context, baseURL, key string) ([]ModelOption, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("x-opencode-session", "mineagent-webui")
	req.Header.Set("User-Agent", "mineagent/1.0")
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models HTTP %d", res.StatusCode)
	}
	var body struct {
		Data []struct {
			ID            string            `json:"id"`
			Name          string            `json:"name"`
			Created       int64             `json:"created"`
			ContextLength int64             `json:"context_length"`
			Pricing       map[string]string `json:"pricing"`
			Architecture  struct {
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			SupportedParameters []string `json:"supported_parameters"`
			ExpirationDate      string   `json:"expiration_date"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&body); err != nil {
		return nil, err
	}
	if body.Data == nil {
		return nil, fmt.Errorf("models response missing data")
	}
	out := make([]ModelOption, 0, len(body.Data))
	seen := make(map[string]bool)
	for _, m := range body.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		label := strings.TrimSpace(m.Name)
		if label == "" {
			label = id
		}
		opt := ModelOption{ID: id, Label: label, Free: freePricing(m.Pricing, id), Created: m.Created, ContextLength: m.ContextLength}
		if len(m.Architecture.OutputModalities) > 0 && !containsString(m.Architecture.OutputModalities, "text") {
			opt.UnavailableReason = "不支持文本对话"
		} else if m.SupportedParameters != nil && !containsString(m.SupportedParameters, "tools") {
			opt.UnavailableReason = "不支持 Agent 工具调用"
		}
		if expires, err := time.Parse("2006-01-02", m.ExpirationDate); err == nil && !time.Now().Before(expires.Add(24*time.Hour)) {
			opt.UnavailableReason = "模型已下线"
		}
		opt.Unavailable = opt.UnavailableReason != ""
		out = append(out, opt)
	}
	return out, nil
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

type providerCache struct {
	at   time.Time
	opts []ModelOption
}

func (c *Channel) providerOptions(ctx context.Context, p config.Provider) []ModelOption {
	if p.ID == "" || p.BaseURL == "" {
		return nil
	}
	c.mu.Lock()
	cached, ok := c.providerModels[p.ID]
	c.mu.Unlock()
	if ok && time.Since(cached.at) < modelCacheTTL {
		return append([]ModelOption(nil), cached.opts...)
	}
	opts, err := fetchModelOptions(ctx, p.BaseURL, p.APIKey)
	stale := err != nil && ok
	if err != nil {
		c.log.Warn("provider models fetch failed", "provider", p.ID)
		opts = append([]ModelOption(nil), cached.opts...)
		// Cached options are already decorated with provider prefixes.
		for i := range opts {
			opts[i].ID = strings.TrimPrefix(opts[i].ID, p.ID+"/")
		}
	}
	opts = configuredModels(opts, p.Models)
	label := p.Label
	if label == "" {
		label = p.ID
	}
	out := make([]ModelOption, 0, len(opts))
	for _, opt := range opts {
		if p.FreeOnly && !opt.Free {
			continue
		}
		opt.ID = p.ID + "/" + opt.ID
		opt.Provider, opt.ProviderLabel, opt.NeedsKey, opt.Stale = p.ID, label, p.APIKey == "", stale
		out = append(out, opt)
	}
	if err == nil {
		c.mu.Lock()
		if c.providerModels == nil {
			c.providerModels = make(map[string]providerCache)
		}
		c.providerModels[p.ID] = providerCache{at: time.Now(), opts: append([]ModelOption(nil), out...)}
		c.mu.Unlock()
	}
	return out
}

// Explicit configuration remains an allowlist; discovery enriches its metadata.
func configuredModels(discovered []ModelOption, configured []string) []ModelOption {
	if len(configured) == 0 {
		return discovered
	}
	byID := make(map[string]ModelOption, len(discovered))
	for _, opt := range discovered {
		byID[opt.ID] = opt
	}
	out := make([]ModelOption, 0, len(configured))
	seen := make(map[string]bool)
	for _, raw := range configured {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		opt, ok := byID[id]
		if !ok {
			opt = ModelOption{ID: id, Label: id, Free: isFreeModelID(id)}
		}
		out = append(out, opt)
	}
	return out
}

func (c *Channel) modelCatalog(ctx context.Context) []ModelOption {
	label := "默认"
	if u, err := url.Parse(c.modelBaseURL); err == nil && u.Host != "" {
		label = u.Host
	}
	opts := c.models.List(ctx)
	opts = configuredModels(opts, c.modelOptions)
	if len(opts) == 0 && c.model != "" {
		opts = []ModelOption{{ID: c.model, Label: c.model, Free: isFreeModelID(c.model)}}
	}
	for i := range opts {
		opts[i].Provider = "default"
		opts[i].ProviderLabel = label
	}
	for _, p := range c.providers {
		opts = append(opts, c.providerOptions(ctx, p)...)
	}
	// Prefer selectable models, then provider release dates. Unknown dates stay last.
	sort.SliceStable(opts, func(i, j int) bool {
		a, b := opts[i], opts[j]
		aOff, bOff := a.Unavailable || a.NeedsKey, b.Unavailable || b.NeedsKey
		if aOff != bOff {
			return !aOff
		}
		if a.Created != b.Created {
			return a.Created > b.Created
		}
		if a.Free != b.Free {
			return a.Free
		}
		return strings.ToLower(a.Label) < strings.ToLower(b.Label)
	})
	return opts
}

func (c *Channel) refreshModelCatalog() {
	c.models.mu.Lock()
	// Throttle forced refreshes; do not erase the last good catalog on errors.
	if time.Since(c.models.at) > 10*time.Second {
		c.models.at = time.Time{}
	}
	c.models.mu.Unlock()
	c.mu.Lock()
	for id, e := range c.providerModels {
		if time.Since(e.at) > 10*time.Second {
			e.at = time.Time{}
			c.providerModels[id] = e
		}
	}
	c.mu.Unlock()
}

type modelLister struct {
	baseURL, apiKey, dataDir string
	log                      func(string, ...any)
	mu                       sync.Mutex
	fetchMu                  sync.Mutex
	at                       time.Time
	list                     []ModelOption
	probedAt                 time.Time
	unavailable              map[string]bool
	verified                 map[string]bool
	probing                  bool
}

func newModelLister(baseURL, apiKey, dataDir string, logf func(string, ...any)) *modelLister {
	l := &modelLister{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, dataDir: dataDir, log: logf, unavailable: make(map[string]bool), verified: make(map[string]bool)}
	l.loadCache()
	return l
}

type probeCache struct {
	ProbedAt    int64    `json:"probedAt"`
	Unavailable []string `json:"unavailable"`
	Verified    []string `json:"verified,omitempty"`
	BaseURL     string   `json:"baseURL"`
}

func (l *modelLister) cachePath() string { return filepath.Join(l.dataDir, "unavailable.json") }
func (l *modelLister) loadCache() {
	b, err := os.ReadFile(l.cachePath())
	if err != nil {
		return
	}
	var c probeCache
	// Discard legacy/stale results and results from another gateway.
	if json.Unmarshal(b, &c) != nil || c.BaseURL != l.baseURL || time.Since(time.UnixMilli(c.ProbedAt)) > modelProbeTTL {
		return
	}
	l.probedAt = time.UnixMilli(c.ProbedAt)
	for _, m := range c.Unavailable {
		l.unavailable[m] = true
	}
	for _, m := range c.Verified {
		l.verified[m] = true
	}
}

func (l *modelLister) saveCache() {
	if l.dataDir == "" {
		return
	}
	l.mu.Lock()
	c := probeCache{ProbedAt: l.probedAt.UnixMilli(), BaseURL: l.baseURL}
	for m := range l.unavailable {
		c.Unavailable = append(c.Unavailable, m)
	}
	for m := range l.verified {
		c.Verified = append(c.Verified, m)
	}
	l.mu.Unlock()
	if os.MkdirAll(l.dataDir, 0o700) != nil {
		return
	}
	b, _ := json.Marshal(c)
	tmp := l.cachePath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, l.cachePath())
	}
}

// Keep the gateway's existing small background checks, but retry failures after TTL.
// Extra providers are not generation-probed: a broad catalog can contain hundreds of paid models.
func (l *modelLister) probeAsync(models []string) {
	l.mu.Lock()
	if l.probing || time.Since(l.probedAt) < modelProbeTTL {
		l.mu.Unlock()
		return
	}
	l.probing = true
	l.mu.Unlock()
	go func() {
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		var mu sync.Mutex
		bad, good := make(map[string]bool), make(map[string]bool)
		for _, m := range models {
			sem <- struct{}{}
			wg.Add(1)
			go func(model string) {
				defer wg.Done()
				defer func() { <-sem }()
				payload, _ := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "1"}}, "max_tokens": 1})
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/chat/completions", strings.NewReader(string(payload)))
				if err != nil {
					return
				}
				req.Header.Set("Authorization", "Bearer "+l.apiKey)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("User-Agent", "mineagent/1.0")
				req.Header.Set("x-opencode-session", "mineagent-model-probe")
				res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
				if err != nil {
					return
				}
				body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
				_ = res.Body.Close()
				mu.Lock()
				defer mu.Unlock()
				if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusServiceUnavailable {
					bad[model] = true
				}
				if res.StatusCode == http.StatusOK {
					var response struct {
						Choices []json.RawMessage `json:"choices"`
					}
					if json.Unmarshal(body, &response) == nil && len(response.Choices) > 0 {
						good[model] = true
					}
				}
			}(m)
		}
		wg.Wait()
		l.mu.Lock()
		l.unavailable = bad
		l.verified = good
		l.probedAt = time.Now()
		l.mu.Unlock()
		l.saveCache()
		l.mu.Lock()
		l.probing = false
		l.mu.Unlock()
		if l.log != nil {
			l.log("model probe done: %d/%d unavailable", len(bad), len(models))
		}
	}()
}

func (l *modelLister) List(ctx context.Context) []ModelOption {
	l.fetchMu.Lock()
	defer l.fetchMu.Unlock()
	l.mu.Lock()
	opts := append([]ModelOption(nil), l.list...)
	fresh := !l.at.IsZero() && time.Since(l.at) < modelCacheTTL
	l.mu.Unlock()
	stale := false
	if !fresh && l.baseURL != "" {
		fetched, err := fetchModelOptions(ctx, l.baseURL, l.apiKey)
		if err == nil {
			opts = fetched
			l.mu.Lock()
			l.list = append([]ModelOption(nil), opts...)
			l.at = time.Now()
			l.mu.Unlock()
		} else {
			stale = true
			if l.log != nil {
				l.log("web models fetch failed")
			}
		}
	}
	ids := make([]string, 0, len(opts))
	l.mu.Lock()
	for i := range opts {
		opts[i].Stale = stale
		if !opts[i].Unavailable {
			ids = append(ids, opts[i].ID)
		}
		if time.Since(l.probedAt) < modelProbeTTL {
			opts[i].Verified = l.verified[opts[i].ID]
			if l.unavailable[opts[i].ID] {
				opts[i].Unavailable = true
				opts[i].UnavailableReason = "网关暂时无法路由"
			}
		}
	}
	l.mu.Unlock()
	if len(ids) > 0 {
		l.probeAsync(ids)
	}
	return opts
}

func (l *modelLister) fetchedAt() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at.IsZero() {
		return 0
	}
	return l.at.UnixMilli()
}
