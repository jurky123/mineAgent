package webui

import (
	"context"
	"encoding/json"
	"io"
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

// accountPrefs 是网页账号的偏好（+ 菜单里改）：模型与思考强度。
// 存 data/webui/prefs.json，重启不掉；空字符串 = 用默认。
type accountPrefs struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

func (c *Channel) loadPrefs() {
	b, err := os.ReadFile(c.prefsPath)
	if err != nil {
		return
	}
	var m map[string]accountPrefs
	if json.Unmarshal(b, &m) != nil {
		return
	}
	c.mu.Lock()
	for k, v := range m {
		if ValidAccountName(k) {
			c.prefs[k] = v
		}
	}
	c.mu.Unlock()
}

func (c *Channel) savePrefs() {
	c.mu.Lock()
	m := make(map[string]accountPrefs, len(c.prefs))
	for k, v := range c.prefs {
		m[k] = v
	}
	c.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(c.prefsPath), 0o700); err != nil {
		c.log.Warn("web prefs mkdir", "err", err)
		return
	}
	b, _ := json.Marshal(m)
	tmp := c.prefsPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		c.log.Warn("web prefs write", "err", err)
		return
	}
	if err := os.Rename(tmp, c.prefsPath); err != nil {
		c.log.Warn("web prefs rename", "err", err)
	}
}

func (c *Channel) prefsOf(name string) accountPrefs {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.prefs[name]
}

func (c *Channel) setPrefs(name string, p accountPrefs) {
	c.mu.Lock()
	c.prefs[name] = p
	c.mu.Unlock()
	c.savePrefs()
}

// 网页支持的思考强度（reasoning_effort），空 = 模型默认。
var effortLevels = []string{"low", "medium", "high"}

func validEffort(v string) bool {
	if v == "" {
		return true
	}
	for _, e := range effortLevels {
		if v == e {
			return true
		}
	}
	return false
}

// ---------- 模型列表：默认提供商 + 额外提供商（OpenRouter 等），带免费标记 ----------

// ModelOption 是给 UI 的模型条目。
type ModelOption struct {
	ID            string `json:"id"`       // 选择器：默认提供商就是模型名；额外提供商是 "<id>/<模型名>"
	Label         string `json:"label"`    // 展示名
	Provider      string `json:"provider"` // "default" 或 provider id
	ProviderLabel string `json:"providerLabel"`
	Free          bool   `json:"free"`
	NeedsKey      bool   `json:"needsKey,omitempty"`
	Unavailable   bool   `json:"unavailable,omitempty"`
}

// isFreeModelID 免费模型的启发式判断（网关不给定价，OpenRouter 会给定价另算）：
// 常见命名 -free / :free / contributor。
func isFreeModelID(id string) bool {
	l := strings.ToLower(id)
	return strings.Contains(l, "-free") || strings.HasSuffix(l, ":free") ||
		strings.Contains(l, "contributor")
}

type providerCache struct {
	at   time.Time
	opts []ModelOption
}

// providerOptions 拉额外提供商的模型（缓存 1 小时）。
// OpenRouter 的 /models 带 pricing，能准确判免费；没有定价就退回命名启发式。
func (c *Channel) providerOptions(ctx context.Context, p config.Provider) []ModelOption {
	if p.ID == "" || p.BaseURL == "" {
		return nil
	}
	c.mu.Lock()
	if e, ok := c.providerModels[p.ID]; ok && time.Since(e.at) < time.Hour {
		opts := append([]ModelOption(nil), e.opts...)
		c.mu.Unlock()
		return opts
	}
	c.mu.Unlock()

	build := func(rawID, name string, pricingPrompt, pricingCompletion *string, hasPricing bool) ModelOption {
		free := isFreeModelID(rawID)
		if hasPricing {
			free = pricingPrompt != nil && pricingCompletion != nil && *pricingPrompt == "0" && *pricingCompletion == "0"
		}
		label := strings.TrimSpace(name)
		if label == "" {
			label = rawID
		}
		return ModelOption{
			ID: p.ID + "/" + rawID, Label: label, Provider: p.ID,
			ProviderLabel: p.Label, Free: free, NeedsKey: p.APIKey == "",
		}
	}
	var opts []ModelOption
	if len(p.Models) > 0 {
		for _, m := range p.Models {
			opts = append(opts, build(m, m, nil, nil, false))
		}
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.BaseURL, "/")+"/models", nil)
		if err != nil {
			return nil
		}
		if p.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.APIKey)
		}
		req.Header.Set("x-opencode-session", "mineagent-webui")
		res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			c.log.Warn("provider models fetch failed", "provider", p.ID, "err", err)
			return nil
		}
		defer res.Body.Close()
		var body struct {
			Data []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Pricing *struct {
					Prompt     string `json:"prompt"`
					Completion string `json:"completion"`
				} `json:"pricing"`
			} `json:"data"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			c.log.Warn("provider models decode failed", "provider", p.ID, "err", err)
			return nil
		}
		for _, m := range body.Data {
			if m.ID == "" {
				continue
			}
			var pp, pc *string
			has := false
			if m.Pricing != nil {
				has = true
				pp, pc = &m.Pricing.Prompt, &m.Pricing.Completion
			}
			opt := build(m.ID, m.Name, pp, pc, has)
			if p.FreeOnly && !opt.Free {
				continue
			}
			opts = append(opts, opt)
		}
	}
	sort.SliceStable(opts, func(i, j int) bool {
		if opts[i].Free != opts[j].Free {
			return opts[i].Free
		}
		return strings.ToLower(opts[i].Label) < strings.ToLower(opts[j].Label)
	})
	c.mu.Lock()
	if c.providerModels == nil {
		c.providerModels = make(map[string]providerCache)
	}
	c.providerModels[p.ID] = providerCache{at: time.Now(), opts: opts}
	c.mu.Unlock()
	return opts
}

// modelCatalog 汇总默认提供商 + 额外提供商的模型列表（给 UI 的完整目录）。
func (c *Channel) modelCatalog(ctx context.Context) []ModelOption {
	label := "默认"
	if u, err := url.Parse(c.modelBaseURL); err == nil && u.Host != "" {
		label = u.Host
	}
	bad := map[string]bool{}
	for _, m := range c.models.Unavailable() {
		bad[m] = true
	}
	var out []ModelOption
	for _, id := range c.availableModels(ctx) {
		out = append(out, ModelOption{
			ID: id, Label: id, Provider: "default", ProviderLabel: label,
			Free: isFreeModelID(id), Unavailable: bad[id],
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Free != out[j].Free {
			return out[i].Free
		}
		return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label)
	})
	for _, p := range c.providers {
		out = append(out, c.providerOptions(ctx, p)...)
	}
	return out
}

// ---------- 模型列表：优先 config.model.options，否则拉网关 GET /models（带缓存） ----------

type modelLister struct {
	baseURL, apiKey string
	dataDir         string
	log             func(format string, args ...any)

	mu   sync.Mutex
	at   time.Time
	list []string
	// 可用性探测：网关 /models 会列出实际不可路由的模型（调用返回 503），
	// 后台用 1-token 请求探一遍并缓存，界面里把不可用的灰掉。
	probedAt    time.Time
	unavailable map[string]bool
	probing     bool
}

func newModelLister(baseURL, apiKey, dataDir string, logf func(string, ...any)) *modelLister {
	l := &modelLister{
		baseURL:     strings.TrimRight(baseURL, "/"),
		apiKey:      apiKey,
		dataDir:     dataDir,
		log:         logf,
		unavailable: make(map[string]bool),
	}
	l.loadCache()
	return l
}

// Unavailable 返回探测确认不可用的模型（6 小时内有效；结果落盘，重启不丢）。
func (l *modelLister) Unavailable() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.probedAt) > 6*time.Hour {
		return nil
	}
	out := make([]string, 0, len(l.unavailable))
	for m := range l.unavailable {
		out = append(out, m)
	}
	return out
}

// probePath 探测结果落盘路径（相对 dataDir）。
func (l *modelLister) cachePath() string { return filepath.Join(l.dataDir, "unavailable.json") }

type probeCache struct {
	ProbedAt    int64    `json:"probedAt"`
	Unavailable []string `json:"unavailable"`
}

// loadCache 启动时读取上次探测结果。
func (l *modelLister) loadCache() {
	b, err := os.ReadFile(l.cachePath())
	if err != nil {
		return
	}
	var c probeCache
	if json.Unmarshal(b, &c) != nil {
		return
	}
	l.mu.Lock()
	l.probedAt = time.UnixMilli(c.ProbedAt)
	for _, m := range c.Unavailable {
		l.unavailable[m] = true
	}
	l.mu.Unlock()
}

// saveCacheLocked 调用了要持有锁或先取快照。
func (l *modelLister) saveCache() {
	l.mu.Lock()
	c := probeCache{ProbedAt: l.probedAt.UnixMilli()}
	for m := range l.unavailable {
		c.Unavailable = append(c.Unavailable, m)
	}
	l.mu.Unlock()
	if err := os.MkdirAll(l.dataDir, 0o700); err != nil {
		return
	}
	b, _ := json.Marshal(c)
	tmp := l.cachePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, l.cachePath())
}

// probeAsync 后台探测模型可用性（并发 4，1-token 请求，失败即认为不可用）。
func (l *modelLister) probeAsync(models []string) {
	l.mu.Lock()
	if l.probing || time.Since(l.probedAt) < 20*time.Minute {
		l.mu.Unlock()
		return
	}
	l.probing = true
	l.mu.Unlock()

	go func() {
		defer func() {
			l.mu.Lock()
			l.probing = false
			l.probedAt = time.Now()
			l.mu.Unlock()
		}()
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		bad := make(map[string]bool)
		var mu sync.Mutex
		l.mu.Lock()
		knownBad := make(map[string]bool, len(l.unavailable))
		for m := range l.unavailable {
			knownBad[m] = true
		}
		l.mu.Unlock()
		for _, m := range models {
			if knownBad[m] {
				bad[m] = true
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(model string) {
				defer wg.Done()
				defer func() { <-sem }()
				payload, _ := json.Marshal(map[string]any{
					"model":      model,
					"messages":   []map[string]string{{"role": "user", "content": "1"}},
					"max_tokens": 1,
				})
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/chat/completions", strings.NewReader(string(payload)))
				if err != nil {
					return
				}
				req.Header.Set("Authorization", "Bearer "+l.apiKey)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("x-opencode-session", "mineagent-model-probe")
				res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
				if err != nil {
					return
				}
				_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
				_ = res.Body.Close()
				if res.StatusCode == http.StatusServiceUnavailable || res.StatusCode == http.StatusNotFound {
					mu.Lock()
					bad[model] = true
					mu.Unlock()
				}
			}(m)
		}
		wg.Wait()
		l.mu.Lock()
		l.unavailable = bad
		l.mu.Unlock()
		l.saveCache()
		l.log("model probe done: %d/%d unavailable", len(bad), len(models))
	}()
}

// List 返回可选模型：网关失败时返回 nil（调用方回退到当前默认模型）。
func (l *modelLister) List(ctx context.Context) []string {
	l.mu.Lock()
	if len(l.list) > 0 && time.Since(l.at) < 10*time.Minute {
		out := append([]string(nil), l.list...)
		l.mu.Unlock()
		return out
	}
	l.mu.Unlock()
	if l.baseURL == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.baseURL+"/models", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+l.apiKey)
	req.Header.Set("x-opencode-session", "mineagent-webui")
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		l.log("web models fetch failed: %v", err)
		return nil
	}
	defer res.Body.Close()
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || len(body.Data) == 0 {
		return nil
	}
	out := make([]string, 0, len(body.Data))
	for _, d := range body.Data {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	l.mu.Lock()
	l.list = out
	l.at = time.Now()
	l.mu.Unlock()
	l.probeAsync(append([]string(nil), out...))
	return append([]string(nil), out...)
}

// availableModels 给 UI 的可选模型列表（保序）。
func (c *Channel) availableModels(ctx context.Context) []string {
	if len(c.modelOptions) > 0 {
		return c.modelOptions
	}
	if l := c.models.List(ctx); len(l) > 0 {
		return l
	}
	if c.model != "" {
		return []string{c.model}
	}
	return nil
}
