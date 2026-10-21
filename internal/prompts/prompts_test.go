package prompts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	m := New(filepath.Join(t.TempDir(), "prompts.json"))
	m.Register(Service{ID: "web", Name: "网页", DefaultText: func() string { return "能力-web" }})
	m.Register(Service{ID: "minecraft", Name: "MC", DefaultText: func() string { return "能力-mc" }})
	return m
}

func TestLegacyTemplatesUpgradeWithoutOverwritingEdits(t *testing.T) {
	m := testManager(t)
	old := legacyPersonas["default"]
	old.ID, old.Builtin = "default", true
	edited := legacyPersonas["tech"]
	edited.ID, edited.Builtin = "tech", true
	edited.Desc = "管理员自定义简介"
	snap := snapshot{Personas: []Persona{old, edited, {ID: "custom", Name: "自定义", Text: "保持我的正文"}},
		ServicePersona: map[string]string{"web": "tech"}, UserPersona: map[string]string{"reader": "default"},
		Global: map[string]CapabilityOverride{"web": {Enabled: true, Text: "自定义渠道规则"}}}
	b, _ := json.Marshal(snap)
	if err := os.WriteFile(m.path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	for _, p := range m.Personas() {
		if p.ID == "default" && (p.Text == old.Text || p.Desc == old.Desc) {
			t.Fatal("untouched legacy template not upgraded")
		}
		if p.ID == "tech" && (p.Text != edited.Text || p.Desc != edited.Desc) {
			t.Fatal("edited built-in template overwritten")
		}
		if p.ID == "custom" && p.Text != "保持我的正文" {
			t.Fatal("custom template overwritten")
		}
	}
	if m.ServicePersona("web") != "tech" || m.UserPersona("reader") != "default" || m.Global("web").Text != "自定义渠道规则" {
		t.Fatal("existing selections or capability override changed")
	}
}

func TestServiceSettingsSaveAndRollback(t *testing.T) {
	m := testManager(t)
	if err := m.SetServiceSettings("web", CapabilityOverride{Enabled: true, Text: "新规则"}, "tech"); err != nil {
		t.Fatal(err)
	}
	if m.ServicePersona("web") != "tech" || m.Global("web").Text != "新规则" {
		t.Fatal("settings not applied together")
	}
	for _, change := range []struct {
		id       string
		override CapabilityOverride
		persona  string
	}{
		{"web", CapabilityOverride{Enabled: true, Text: "不应生效"}, "missing"},
		{"missing", CapabilityOverride{Enabled: true, Text: "不应生效"}, "default"},
		{"web", CapabilityOverride{Enabled: true, Text: "  "}, "default"},
	} {
		if err := m.SetServiceSettings(change.id, change.override, change.persona); err == nil {
			t.Fatal("invalid settings accepted")
		}
		if m.ServicePersona("web") != "tech" || m.Global("web").Text != "新规则" {
			t.Fatal("invalid update partially applied")
		}
	}
	// 将持久化目标改为目录，强制 Rename 失败，内存中的生效值也必须回滚。
	m.path = t.TempDir()
	if err := m.SetServiceSettings("web", CapabilityOverride{Enabled: true, Text: "写盘失败"}, "teacher"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if m.ServicePersona("web") != "tech" || m.Global("web").Text != "新规则" {
		t.Fatal("persistence failure changed active settings")
	}
}

func TestPersonaSaveFailurePreservesActiveSettings(t *testing.T) {
	m := testManager(t)
	if err := m.SetUserPersona("reader", "tech"); err != nil {
		t.Fatal(err)
	}
	before := m.Resolve("web", "reader", "规则")
	m.path = t.TempDir()
	if err := m.SetUserPersona("reader", "teacher"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if err := m.SetUserPersona("reader", ""); err == nil {
		t.Fatal("expected persistence failure")
	}
	if m.UserPersona("reader") != "tech" {
		t.Fatal("failed selection changed active persona")
	}
	if _, err := m.SavePersona(Persona{ID: "tech", Name: "新名称", Text: "不应生效"}); err == nil {
		t.Fatal("expected persistence failure")
	}
	if got := m.Resolve("web", "reader", "规则"); got != before {
		t.Fatal("failed edit changed active template")
	}
	n := len(m.Personas())
	if _, err := m.SavePersona(Persona{Name: "新模板", Text: "不应保存"}); err == nil {
		t.Fatal("expected persistence failure")
	}
	if len(m.Personas()) != n {
		t.Fatal("failed creation left a template behind")
	}
	if err := m.DeletePersona("tech"); err == nil {
		t.Fatal("expected reset persistence failure")
	}
	if got := m.Resolve("web", "reader", "规则"); got != before {
		t.Fatal("failed reset changed active template")
	}
}

func TestPersonaDeleteFailurePreservesReferences(t *testing.T) {
	m := testManager(t)
	p, err := m.SavePersona(Persona{Name: "自定义", Text: "正在使用的模板"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetUserPersona("reader", p.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.SetServicePersona("web", p.ID); err != nil {
		t.Fatal(err)
	}
	m.path = t.TempDir()
	if err := m.DeletePersona(p.ID); err == nil {
		t.Fatal("expected delete persistence failure")
	}
	if m.UserPersona("reader") != p.ID || m.ServicePersona("web") != p.ID || !strings.Contains(m.Resolve("web", "reader", "规则"), p.Text) {
		t.Fatal("failed deletion removed active template or references")
	}
}

func TestResolveComposition(t *testing.T) {
	m := testManager(t)

	// 默认：默认助手人设 + 能力
	got := m.Resolve("web", "jzk", "能力-web")
	if !strings.HasPrefix(got, "你是 MineAgent") || !strings.HasSuffix(got, "能力-web") {
		t.Fatalf("默认组合不对: %q", got)
	}

	// MC 默认人设是"MC 老玩家"
	gotMC := m.Resolve("minecraft", "", "能力-mc")
	if !strings.Contains(gotMC, "Minecraft 的游戏助手") || !strings.HasSuffix(gotMC, "能力-mc") {
		t.Fatalf("MC 默认人设不对: %q", gotMC)
	}

	// 全局能力覆盖（完全替换默认能力）
	if err := m.SetCapabilities("web", CapabilityOverride{Enabled: true, Text: "能力-覆盖"}); err != nil {
		t.Fatal(err)
	}
	got = m.Resolve("web", "jzk", "能力-web")
	if !strings.HasSuffix(got, "能力-覆盖") || strings.Contains(got, "能力-web") {
		t.Fatalf("能力覆盖不对: %q", got)
	}

	// 服务人设切换
	if err := m.SetServicePersona("web", "tech"); err != nil {
		t.Fatal(err)
	}
	got = m.Resolve("web", "jzk", "能力-web")
	if !strings.Contains(got, "资深工程师搭档") {
		t.Fatalf("服务人设未生效: %q", got)
	}

	// 用户自选人设覆盖服务人设（仅网页；IM 传 user="" 时不受影响）
	if err := m.SetUserPersona("jzk", "snarky"); err != nil {
		t.Fatal(err)
	}
	if got = m.Resolve("web", "jzk", "能力-web"); !strings.Contains(got, "损友") {
		t.Fatalf("用户人设未生效: %q", got)
	}
	if got = m.Resolve("web", "other", "能力-web"); !strings.Contains(got, "资深工程师搭档") {
		t.Fatalf("他人不应看到本人人设: %q", got)
	}
	if got = m.Resolve("web", "", "能力-web"); !strings.Contains(got, "资深工程师搭档") {
		t.Fatalf("无用户身份应回退服务人设: %q", got)
	}

	// 空值 = 恢复默认
	if err := m.SetUserPersona("jzk", ""); err != nil {
		t.Fatal(err)
	}
	if got = m.Resolve("web", "jzk", "能力-web"); !strings.Contains(got, "资深工程师搭档") {
		t.Fatalf("清空用户人设后应回退服务人设: %q", got)
	}
	if err := m.SetServicePersona("web", ""); err != nil {
		t.Fatal(err)
	}
	if got = m.Resolve("web", "", "能力-web"); !strings.Contains(got, "你是 MineAgent") {
		t.Fatalf("清空服务人设后应回退默认人设: %q", got)
	}
}

func TestPersonaCRUD(t *testing.T) {
	m := testManager(t)

	p, err := m.SavePersona(Persona{Name: "新角色", Desc: "测试", Text: "你是测试角色。"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || p.Builtin {
		t.Fatalf("新建人设异常: %+v", p)
	}
	if err := m.SetServicePersona("web", p.ID); err != nil {
		t.Fatal(err)
	}

	// 更新
	p.Text = "你是更新后的角色。"
	if _, err := m.SavePersona(p); err != nil {
		t.Fatal(err)
	}
	if got := m.Resolve("web", "", "能力-web"); !strings.Contains(got, "更新后的角色") {
		t.Fatalf("更新人设未生效: %q", got)
	}

	// 删除自定义：同时清掉引用，回退默认
	if err := m.DeletePersona(p.ID); err != nil {
		t.Fatal(err)
	}
	if got := m.Resolve("web", "", "能力-web"); !strings.Contains(got, "你是 MineAgent") {
		t.Fatalf("删除后应回退默认人设: %q", got)
	}

	// 编辑内置后删除 = 恢复内置正文
	if _, err := m.SavePersona(Persona{ID: "default", Name: "默认助手", Text: "被改过的默认"}); err != nil {
		t.Fatal(err)
	}
	if got := m.Resolve("web", "", "能力-web"); !strings.Contains(got, "被改过的默认") {
		t.Fatalf("内置编辑未生效: %q", got)
	}
	if err := m.DeletePersona("default"); err != nil {
		t.Fatal(err)
	}
	if got := m.Resolve("web", "", "能力-web"); strings.Contains(got, "被改过的默认") {
		t.Fatalf("内置删除后应恢复: %q", got)
	}
}

func TestValidation(t *testing.T) {
	m := testManager(t)
	if _, err := m.SavePersona(Persona{Name: "", Text: "x"}); err == nil {
		t.Fatal("空名称应报错")
	}
	if _, err := m.SavePersona(Persona{Name: "x", Text: ""}); err == nil {
		t.Fatal("空正文应报错")
	}
	if _, err := m.SavePersona(Persona{Name: "x", Text: strings.Repeat("a", MaxPersonaLen+1)}); err == nil {
		t.Fatal("超长正文应报错")
	}
	if err := m.SetServicePersona("web", "nope"); err == nil {
		t.Fatal("不存在的人设应报错")
	}
	if err := m.SetServicePersona("nope", "default"); err == nil {
		t.Fatal("未知服务应报错")
	}
	if err := m.SetCapabilities("web", CapabilityOverride{Enabled: true, Text: strings.Repeat("a", MaxCapabilityLen+1)}); err == nil {
		t.Fatal("超长能力应报错")
	}
	if err := m.SetCapabilities("web", CapabilityOverride{Enabled: true, Text: "  "}); err != nil {
		t.Fatal(err)
	}
	if m.Global("web").Enabled {
		t.Fatal("空白文本不应启用")
	}
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompts.json")
	m := New(path)
	m.Register(Service{ID: "web", DefaultText: func() string { return "能力" }})
	if _, err := m.SavePersona(Persona{Name: "私有", Text: "你是私有角色。"}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetServicePersona("web", "tech"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetUserPersona("jzk", "snarky"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetCapabilities("web", CapabilityOverride{Enabled: true, Text: "覆盖"}); err != nil {
		t.Fatal(err)
	}

	m2 := New(path)
	m2.Register(Service{ID: "web", DefaultText: func() string { return "能力" }})
	if err := m2.Load(); err != nil {
		t.Fatal(err)
	}
	got := m2.Resolve("web", "jzk", "能力")
	if !strings.Contains(got, "损友") || !strings.HasSuffix(got, "覆盖") {
		t.Fatalf("重启后组合不对: %q", got)
	}
	if len(m2.Personas()) != len(builtinPersonas)+1 {
		t.Fatalf("人设数量不对: %d", len(m2.Personas()))
	}
}
