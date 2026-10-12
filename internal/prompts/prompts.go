// Package prompts 管理各"服务"（通道）的系统提示词覆盖：
//
//   - 内置默认：各通道常量，由 main 启动时注册（用于展示与兜底）
//   - 全局覆盖：管理员在「我的」页编辑，启用后**完全替换**默认
//   - 用户追加：每个账号为自己的网页会话追加一小段（拼在最终提示词末尾）
//
// 生效链：默认 →（全局覆盖启用则替换）→ 追加用户内容。
// 数据存 data/webui/prompts.json（原子写）；取值发生在每次构造请求时，改动即时生效。
package prompts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	MaxGlobalLen = 20000
	MaxUserLen   = 4000
)

// Service 是一个可覆盖提示词的服务（通道）。
type Service struct {
	ID          string
	Name        string
	Desc        string
	DefaultText func() string
}

// Override 是管理员为某服务设置的全局覆盖（Enabled 且 Text 非空才生效）。
type Override struct {
	Enabled bool   `json:"enabled"`
	Text    string `json:"text,omitempty"`
}

type snapshot struct {
	Global map[string]Override          `json:"global,omitempty"`
	Users  map[string]map[string]string `json:"users,omitempty"`
}

// Manager 持有服务注册表与覆盖数据。
type Manager struct {
	mu     sync.RWMutex
	path   string
	order  []Service
	index  map[string]Service
	global map[string]Override
	users  map[string]map[string]string
}

func New(path string) *Manager {
	return &Manager{
		path:   path,
		index:  make(map[string]Service),
		global: make(map[string]Override),
		users:  make(map[string]map[string]string),
	}
}

// Register 注册一个服务（重复注册第一个为准）。
func (m *Manager) Register(s Service) {
	if s.ID == "" || s.DefaultText == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.index[s.ID]; dup {
		return
	}
	m.index[s.ID] = s
	m.order = append(m.order, s)
}

func (m *Manager) Load() error {
	b, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var snap snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return fmt.Errorf("parse prompts: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if snap.Global != nil {
		m.global = snap.Global
	}
	if snap.Users != nil {
		m.users = snap.Users
	}
	return nil
}

func (m *Manager) saveLocked() error {
	b, err := json.Marshal(snapshot{Global: m.global, Users: m.users})
	if err != nil {
		return err
	}
	if dir := filepath.Dir(m.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *Manager) Services() []Service {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Service, len(m.order))
	copy(out, m.order)
	return out
}

func (m *Manager) DefaultText(id string) string {
	m.mu.RLock()
	svc, ok := m.index[id]
	m.mu.RUnlock()
	if !ok {
		return ""
	}
	return svc.DefaultText()
}

func (m *Manager) Global(id string) Override {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.global[id]
}

func (m *Manager) GlobalEnabled(id string) bool {
	g := m.Global(id)
	return g.Enabled && strings.TrimSpace(g.Text) != ""
}

func (m *Manager) SetGlobal(id string, o Override) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.index[id]; !ok {
		return fmt.Errorf("未知服务 %s", id)
	}
	o.Text = strings.TrimSpace(o.Text)
	if len([]rune(o.Text)) > MaxGlobalLen {
		return fmt.Errorf("提示词太长（上限 %d 字）", MaxGlobalLen)
	}
	if o.Text == "" {
		o.Enabled = false
	}
	m.global[id] = o
	return m.saveLocked()
}

func (m *Manager) ResetGlobal(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.index[id]; !ok {
		return fmt.Errorf("未知服务 %s", id)
	}
	delete(m.global, id)
	return m.saveLocked()
}

func (m *Manager) UserAppend(id, user string) string {
	if user == "" {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.users[user][id]
}

func (m *Manager) SetUserAppend(id, user, text string) error {
	if user == "" {
		return fmt.Errorf("缺少账号")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.index[id]; !ok {
		return fmt.Errorf("未知服务 %s", id)
	}
	text = strings.TrimSpace(text)
	if len([]rune(text)) > MaxUserLen {
		return fmt.Errorf("追加提示词太长（上限 %d 字）", MaxUserLen)
	}
	if text == "" {
		if m.users[user] != nil {
			delete(m.users[user], id)
			if len(m.users[user]) == 0 {
				delete(m.users, user)
			}
		}
		return m.saveLocked()
	}
	if m.users[user] == nil {
		m.users[user] = make(map[string]string)
	}
	m.users[user][id] = text
	return m.saveLocked()
}

// Resolve 返回某服务某用户的最终系统提示词。
func (m *Manager) Resolve(id, user, defaultText string) string {
	m.mu.RLock()
	base := defaultText
	if g, ok := m.global[id]; ok && g.Enabled && strings.TrimSpace(g.Text) != "" {
		base = strings.TrimSpace(g.Text)
	}
	add := ""
	if user != "" {
		add = strings.TrimSpace(m.users[user][id])
	}
	m.mu.RUnlock()
	if add != "" {
		base = base + "\n\n" + add
	}
	return base
}

// ---------- 进程级默认实例（通道在构造请求时直接调用） ----------

var def *Manager

// Init 加载默认实例，返回的错误只影响日志，不影响返回值可用性。
func Init(path string) (*Manager, error) {
	m := New(path)
	err := m.Load()
	def = m
	return m, err
}

func Def() *Manager { return def }

func Services() []Service {
	if def == nil {
		return nil
	}
	return def.Services()
}

func DefaultText(id string) string {
	if def == nil {
		return ""
	}
	return def.DefaultText(id)
}

func Global(id string) Override {
	if def == nil {
		return Override{}
	}
	return def.Global(id)
}

func GlobalEnabled(id string) bool {
	if def == nil {
		return false
	}
	return def.GlobalEnabled(id)
}

func SetGlobal(id string, o Override) error {
	if def == nil {
		return fmt.Errorf("提示词未初始化")
	}
	return def.SetGlobal(id, o)
}

func ResetGlobal(id string) error {
	if def == nil {
		return fmt.Errorf("提示词未初始化")
	}
	return def.ResetGlobal(id)
}

func UserAppend(id, user string) string {
	if def == nil {
		return ""
	}
	return def.UserAppend(id, user)
}

func SetUserAppend(id, user, text string) error {
	if def == nil {
		return fmt.Errorf("提示词未初始化")
	}
	return def.SetUserAppend(id, user, text)
}

// Resolve 在未初始化时直接返回 defaultText，保证通道行为与以前一致。
func Resolve(id, user, defaultText string) string {
	if def == nil {
		return defaultText
	}
	return def.Resolve(id, user, defaultText)
}
