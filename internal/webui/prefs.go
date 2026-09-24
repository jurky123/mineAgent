package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
