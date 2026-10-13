package webui

import (
	"encoding/json"
	"os"
	"path/filepath"
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
