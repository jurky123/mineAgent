package prompts

import (
	"path/filepath"
	"strings"
	"testing"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	m := New(filepath.Join(t.TempDir(), "prompts.json"))
	m.Register(Service{ID: "web", Name: "网页", Desc: "网页聊天", DefaultText: func() string { return "默认-web" }})
	m.Register(Service{ID: "qq", Name: "QQ", Desc: "QQ", DefaultText: func() string { return "默认-qq" }})
	return m
}

func TestResolveChain(t *testing.T) {
	m := testManager(t)

	if got := m.Resolve("web", "jzk", "默认-web"); got != "默认-web" {
		t.Fatalf("默认: %q", got)
	}

	if err := m.SetGlobal("web", Override{Enabled: true, Text: "全局-web"}); err != nil {
		t.Fatal(err)
	}
	if got := m.Resolve("web", "jzk", "默认-web"); got != "全局-web" {
		t.Fatalf("全局替换: %q", got)
	}
	if got := m.Resolve("qq", "jzk", "默认-qq"); got != "默认-qq" {
		t.Fatalf("其它服务不受影响: %q", got)
	}

	if err := m.SetUserAppend("web", "jzk", "我的补充"); err != nil {
		t.Fatal(err)
	}
	want := "全局-web\n\n我的补充"
	if got := m.Resolve("web", "jzk", "默认-web"); got != want {
		t.Fatalf("全局+追加: %q want %q", got, want)
	}
	// IM 通道没有用户身份（user=""），只拿全局
	if got := m.Resolve("web", "", "默认-web"); got != "全局-web" {
		t.Fatalf("无用户身份不应追加: %q", got)
	}
	// 别的账号不共享追加
	if got := m.Resolve("web", "other", "默认-web"); got != "全局-web" {
		t.Fatalf("他人不应看到追加: %q", got)
	}

	// 全局未启用时，用户追加拼在默认之后
	if err := m.SetGlobal("qq", Override{Enabled: false, Text: "不该生效"}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetUserAppend("qq", "jzk", "补充2"); err != nil {
		t.Fatal(err)
	}
	if got := m.Resolve("qq", "jzk", "默认-qq"); got != "默认-qq\n\n补充2" {
		t.Fatalf("默认+追加: %q", got)
	}
}

func TestGlobalValidation(t *testing.T) {
	m := testManager(t)
	if err := m.SetGlobal("nope", Override{Enabled: true, Text: "x"}); err == nil {
		t.Fatal("未知服务应报错")
	}
	if err := m.SetGlobal("web", Override{Enabled: true, Text: strings.Repeat("a", MaxGlobalLen+1)}); err == nil {
		t.Fatal("超长应报错")
	}
	// 空白文本视为未启用
	if err := m.SetGlobal("web", Override{Enabled: true, Text: "   "}); err != nil {
		t.Fatal(err)
	}
	if m.GlobalEnabled("web") {
		t.Fatal("空白文本不应被视为启用")
	}
	if err := m.SetUserAppend("web", "jzk", strings.Repeat("b", MaxUserLen+1)); err == nil {
		t.Fatal("用户追加超长应报错")
	}
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompts.json")
	m := New(path)
	m.Register(Service{ID: "web", DefaultText: func() string { return "默认" }})
	if err := m.SetGlobal("web", Override{Enabled: true, Text: "覆盖"}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetUserAppend("web", "jzk", "追加"); err != nil {
		t.Fatal(err)
	}

	m2 := New(path)
	m2.Register(Service{ID: "web", DefaultText: func() string { return "默认" }})
	if err := m2.Load(); err != nil {
		t.Fatal(err)
	}
	if got := m2.Resolve("web", "jzk", "默认"); got != "覆盖\n\n追加" {
		t.Fatalf("重启后: %q", got)
	}
	// 清空追加后应删除该用户的记录
	if err := m2.SetUserAppend("web", "jzk", ""); err != nil {
		t.Fatal(err)
	}
	if got := m2.UserAppend("web", "jzk"); got != "" {
		t.Fatalf("清空后: %q", got)
	}
	if err := m2.ResetGlobal("web"); err != nil {
		t.Fatal(err)
	}
	if m2.GlobalEnabled("web") {
		t.Fatal("重置后不应还有覆盖")
	}
}

func TestRegisterDedupAndOrder(t *testing.T) {
	m := testManager(t)
	m.Register(Service{ID: "web", Name: "第二个", DefaultText: func() string { return "x" }})
	svc := m.Services()
	if len(svc) != 2 || svc[0].ID != "web" || svc[1].ID != "qq" {
		t.Fatalf("顺序或去重不对: %+v", svc)
	}
	if svc[0].Name != "网页" {
		t.Fatalf("重复注册应保留第一个: %q", svc[0].Name)
	}
}
