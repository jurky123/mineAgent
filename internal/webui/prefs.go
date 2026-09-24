package webui

import (
	"context"
	"encoding/json"
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
	log             func(format string, args ...any)

	mu   sync.Mutex
	at   time.Time
	list []string
}

func newModelLister(baseURL, apiKey string, logf func(string, ...any)) *modelLister {
	return &modelLister{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, log: logf}
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
