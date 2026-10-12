package prompts

import (
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

func TestResolveComposition(t *testing.T) {
	m := testManager(t)

	// 默认：默认助手人设 + 能力
	got := m.Resolve("web", "jzk", "能力-web")
	if !strings.HasPrefix(got, "你是 MineAgent") || !strings.HasSuffix(got, "能力-web") {
		t.Fatalf("默认组合不对: %q", got)
	}

	// MC 默认人设是"MC 老玩家"
	gotMC := m.Resolve("minecraft", "", "能力-mc")
	if !strings.Contains(gotMC, "老玩家") || !strings.HasSuffix(gotMC, "能力-mc") {
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
