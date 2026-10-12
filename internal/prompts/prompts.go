// Package prompts 管理系统提示词的两层：
//
//   - 系统提示词（能力范围）：按服务的可编辑覆盖（管理员），完全替换各通道内置的
//     "能力/规则"文本；未覆盖时用内置默认。
//   - 人设模板：一组可切换的角色（名称 + 正文），管理员维护公共模板，
//     每个服务全局选一条，网页用户还能给自己选一条（仅本人、仅网页）。
//
// 最终系统提示词 = 选中的人设正文 + "\n\n" + 服务系统提示词（能力范围）。
// 人设只负责角色/语气，安全准则与渠道规则仍由"系统提示词"层保证，换人设不会丢能力。
//
// 数据存 data/webui/prompts.json（原子写）；每次构造请求时取值，改动即时生效。
package prompts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	MaxCapabilityLen = 20000
	MaxPersonaLen    = 8000
	MaxNameLen       = 40
	MaxDescLen       = 200
)

// Service 是一个可配置的服务（通道）。
type Service struct {
	ID          string
	Name        string
	Desc        string
	DefaultText func() string // 内置"系统提示词（能力范围）"
}

// CapabilityOverride 是管理员为某服务设置的系统提示词覆盖。
type CapabilityOverride struct {
	Enabled bool   `json:"enabled"`
	Text    string `json:"text,omitempty"`
}

// Persona 是一条人设模板。
type Persona struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Desc    string `json:"desc,omitempty"`
	Text    string `json:"text"`
	Builtin bool   `json:"builtin,omitempty"`
}

type snapshot struct {
	Global         map[string]CapabilityOverride `json:"global,omitempty"`
	Personas       []Persona                     `json:"personas,omitempty"`
	ServicePersona map[string]string             `json:"servicePersona,omitempty"`
	UserPersona    map[string]string             `json:"userPersona,omitempty"`
}

// builtinPersonas 是内置人设模板（可编辑；删除=恢复这里的内容）。
var builtinPersonas = []Persona{
	{ID: "default", Name: "默认助手", Desc: "通用、能动手",
		Text: "你是 MineAgent，一个能动手的 agent（写代码/执行、查资料、画图、收发文件、联网搜索、设提醒）。"},
	{ID: "mc-veteran", Name: "MC 老玩家", Desc: "服里随和的老玩家",
		Text: "你是服务器里的老玩家 AI，说话随和、简短、接地气，像个熟络的服友。"},
	{ID: "concise", Name: "简洁客服", Desc: "只给答案和下一步",
		Text: "你是高效的客服：先给答案和下一步，不寒暄、不铺垫、不复述问题。"},
	{ID: "tech", Name: "技术搭档", Desc: "结论 + 步骤 + 代码",
		Text: "你是资深工程师搭档：先给结论，再给可执行步骤和代码，指出坑与风险。"},
	{ID: "snarky", Name: "毒舌损友", Desc: "爱吐槽但不冒犯",
		Text: "你是嘴上不饶人的损友：会吐槽调侃，但不人身攻击、不冒犯、不传播负面情绪，正事照样办。"},
	{ID: "translator", Name: "中英翻译", Desc: "只输出译文",
		Text: "你是翻译：只输出译文（未指定目标语言时做中↔英互译），不加解释、不加客套。"},
	{ID: "teacher", Name: "严谨老师", Desc: "循序讲解",
		Text: "你是耐心的老师：先给直觉再给细节，循序渐进，讲完做个小结。"},
}

// DefaultPersonaFor 返回某服务的默认人设（Minecraft 用"MC 老玩家"，其余"默认助手"）。
func DefaultPersonaFor(service string) string {
	if service == "minecraft" {
		return "mc-veteran"
	}
	return "default"
}

type Manager struct {
	mu     sync.RWMutex
	path   string
	order  []Service
	index  map[string]Service
	global map[string]CapabilityOverride

	personas       []Persona
	personaIndex   map[string]int
	servicePersona map[string]string
	userPersona    map[string]string
}

func New(path string) *Manager {
	m := &Manager{
		path:           path,
		index:          make(map[string]Service),
		global:         make(map[string]CapabilityOverride),
		personaIndex:   make(map[string]int),
		servicePersona: make(map[string]string),
		userPersona:    make(map[string]string),
	}
	m.ensureBuiltins()
	return m
}

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

// ensureBuiltins 保证内置人设存在（保留已存的自定义/编辑内容）。
func (m *Manager) ensureBuiltins() {
	for i, p := range m.personas {
		m.personaIndex[p.ID] = i
	}
	for _, b := range builtinPersonas {
		if _, ok := m.personaIndex[b.ID]; ok {
			continue
		}
		b.Builtin = true
		m.personaIndex[b.ID] = len(m.personas)
		m.personas = append(m.personas, b)
	}
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
	if snap.Personas != nil {
		m.personas = nil
		m.personaIndex = make(map[string]int)
		for _, p := range snap.Personas {
			m.personaIndex[p.ID] = len(m.personas)
			m.personas = append(m.personas, p)
		}
	}
	if snap.ServicePersona != nil {
		m.servicePersona = snap.ServicePersona
	}
	if snap.UserPersona != nil {
		m.userPersona = snap.UserPersona
	}
	m.ensureBuiltins()
	return nil
}

func (m *Manager) saveLocked() error {
	b, err := json.Marshal(snapshot{
		Global:         m.global,
		Personas:       m.personas,
		ServicePersona: m.servicePersona,
		UserPersona:    m.userPersona,
	})
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

// ---------- 服务 / 系统提示词（能力范围） ----------

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

func (m *Manager) Global(id string) CapabilityOverride {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.global[id]
}

func (m *Manager) SetCapabilities(id string, o CapabilityOverride) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.index[id]; !ok {
		return fmt.Errorf("未知服务 %s", id)
	}
	o.Text = strings.TrimSpace(o.Text)
	if len([]rune(o.Text)) > MaxCapabilityLen {
		return fmt.Errorf("系统提示词太长（上限 %d 字）", MaxCapabilityLen)
	}
	if o.Text == "" {
		o.Enabled = false
	}
	m.global[id] = o
	return m.saveLocked()
}

func (m *Manager) ResetCapabilities(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.index[id]; !ok {
		return fmt.Errorf("未知服务 %s", id)
	}
	delete(m.global, id)
	return m.saveLocked()
}

// ---------- 人设模板 ----------

func (m *Manager) Personas() []Persona {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Persona, len(m.personas))
	copy(out, m.personas)
	return out
}

func (m *Manager) PersonaByID(id string) (Persona, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if i, ok := m.personaIndex[id]; ok {
		return m.personas[i], true
	}
	return Persona{}, false
}

// SavePersona 新建（ID 为空）或更新人设。
func (m *Manager) SavePersona(p Persona) (Persona, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Desc = strings.TrimSpace(p.Desc)
	p.Text = strings.TrimSpace(p.Text)
	if p.Name == "" {
		return p, fmt.Errorf("人设名称不能为空")
	}
	if len([]rune(p.Name)) > MaxNameLen {
		return p, fmt.Errorf("名称太长（上限 %d 字）", MaxNameLen)
	}
	if len([]rune(p.Desc)) > MaxDescLen {
		return p, fmt.Errorf("简介太长（上限 %d 字）", MaxDescLen)
	}
	if p.Text == "" {
		return p, fmt.Errorf("人设正文不能为空")
	}
	if len([]rune(p.Text)) > MaxPersonaLen {
		return p, fmt.Errorf("人设正文太长（上限 %d 字）", MaxPersonaLen)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.ID == "" {
		p.ID = fmt.Sprintf("p-%d", time.Now().UnixMilli())
		p.Builtin = false
		m.personaIndex[p.ID] = len(m.personas)
		m.personas = append(m.personas, p)
	} else {
		i, ok := m.personaIndex[p.ID]
		if !ok {
			return p, fmt.Errorf("人设不存在")
		}
		p.Builtin = m.personas[i].Builtin
		m.personas[i] = p
	}
	return p, m.saveLocked()
}

// DeletePersona 删除人设；内置人设不删除，而是恢复内置正文。
func (m *Manager) DeletePersona(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.personaIndex[id]
	if !ok {
		return fmt.Errorf("人设不存在")
	}
	for _, b := range builtinPersonas {
		if b.ID == id {
			b.Builtin = true
			m.personas[i] = b
			return m.saveLocked()
		}
	}
	m.personas = append(m.personas[:i], m.personas[i+1:]...)
	delete(m.personaIndex, id)
	for j := i; j < len(m.personas); j++ {
		m.personaIndex[m.personas[j].ID] = j
	}
	// 清掉正在使用它的人设引用
	for k, v := range m.servicePersona {
		if v == id {
			delete(m.servicePersona, k)
		}
	}
	for k, v := range m.userPersona {
		if v == id {
			delete(m.userPersona, k)
		}
	}
	return m.saveLocked()
}

func (m *Manager) personaText(id string) string {
	if i, ok := m.personaIndex[id]; ok {
		return strings.TrimSpace(m.personas[i].Text)
	}
	return ""
}

func (m *Manager) personaFor(service, user string) string {
	if user != "" {
		if id := m.userPersona[user]; id != "" {
			return id
		}
	}
	if id := m.servicePersona[service]; id != "" {
		return id
	}
	return DefaultPersonaFor(service)
}

func (m *Manager) ServicePersona(service string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if id := m.servicePersona[service]; id != "" {
		return id
	}
	return DefaultPersonaFor(service)
}

func (m *Manager) SetServicePersona(service, personaID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.index[service]; !ok {
		return fmt.Errorf("未知服务 %s", service)
	}
	if personaID == "" { // 空 = 恢复服务默认人设
		delete(m.servicePersona, service)
		return m.saveLocked()
	}
	if _, ok := m.personaIndex[personaID]; !ok {
		return fmt.Errorf("人设不存在")
	}
	m.servicePersona[service] = personaID
	return m.saveLocked()
}

func (m *Manager) UserPersona(user string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.userPersona[user]
}

func (m *Manager) SetUserPersona(user, personaID string) error {
	if user == "" {
		return fmt.Errorf("缺少账号")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if personaID == "" { // 空 = 跟随服务/默认人设
		delete(m.userPersona, user)
		return m.saveLocked()
	}
	if _, ok := m.personaIndex[personaID]; !ok {
		return fmt.Errorf("人设不存在")
	}
	m.userPersona[user] = personaID
	return m.saveLocked()
}

// ---------- 组合 ----------

// Resolve 返回某服务某用户的最终系统提示词。
func (m *Manager) Resolve(service, user, defaultCapabilities string) string {
	m.mu.RLock()
	ptext := m.personaText(m.personaFor(service, user))
	caps := defaultCapabilities
	if g, ok := m.global[service]; ok && g.Enabled && strings.TrimSpace(g.Text) != "" {
		caps = strings.TrimSpace(g.Text)
	}
	m.mu.RUnlock()
	caps = strings.TrimSpace(caps)
	if ptext == "" {
		return caps
	}
	if caps == "" {
		return ptext
	}
	return ptext + "\n\n" + caps
}

// ---------- 进程级默认实例 ----------

var def *Manager

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

func Global(id string) CapabilityOverride {
	if def == nil {
		return CapabilityOverride{}
	}
	return def.Global(id)
}

func SetCapabilities(id string, o CapabilityOverride) error {
	if def == nil {
		return fmt.Errorf("提示词未初始化")
	}
	return def.SetCapabilities(id, o)
}

func ResetCapabilities(id string) error {
	if def == nil {
		return fmt.Errorf("提示词未初始化")
	}
	return def.ResetCapabilities(id)
}

func Personas() []Persona {
	if def == nil {
		return nil
	}
	return def.Personas()
}

func SavePersona(p Persona) (Persona, error) {
	if def == nil {
		return p, fmt.Errorf("提示词未初始化")
	}
	return def.SavePersona(p)
}

func DeletePersona(id string) error {
	if def == nil {
		return fmt.Errorf("提示词未初始化")
	}
	return def.DeletePersona(id)
}

func ServicePersona(service string) string {
	if def == nil {
		return DefaultPersonaFor(service)
	}
	return def.ServicePersona(service)
}

func SetServicePersona(service, personaID string) error {
	if def == nil {
		return fmt.Errorf("提示词未初始化")
	}
	return def.SetServicePersona(service, personaID)
}

func UserPersona(user string) string {
	if def == nil {
		return ""
	}
	return def.UserPersona(user)
}

func SetUserPersona(user, personaID string) error {
	if def == nil {
		return fmt.Errorf("提示词未初始化")
	}
	return def.SetUserPersona(user, personaID)
}

// Resolve 在未初始化时直接返回 defaultCapabilities，保证通道行为与以前一致。
func Resolve(service, user, defaultCapabilities string) string {
	if def == nil {
		return defaultCapabilities
	}
	return def.Resolve(service, user, defaultCapabilities)
}
