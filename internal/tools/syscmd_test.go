package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"mineagent/internal/storage"
)

func testSysStore(t *testing.T) *storage.Store {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMatchSystemCommand(t *testing.T) {
	cases := []struct {
		in      string
		cmd     string
		arg     string
	}{
		{"/help", "help", ""},
		{"/HELP", "help", ""},
		{"帮助", "help", ""},
		{"命令", "help", ""},
		{"你能干什么", "help", ""},
		{"/status", "status", ""},
		{"状态", "status", ""},
		{"/memory", "memory", ""},
		{"/memory clear", "memory_clear", ""},
		{"/memory summary", "memory_summary", ""},
		{"记忆", "memory", ""},
		{"/bind Steve", "bind", "Steve"},
		{"/bind", "bind", ""},
		{"绑定 Steve", "bind", "Steve"},
		{"绑定Steve", "bind", "Steve"},
		{"/unbind", "unbind", ""},
		{"解绑", "unbind", ""},
		{"/myid", "myid", ""},
		{"我是谁", "myid", ""},
		// 非命令：必须返回 ""，继续走 agent。
		{"你好", "", ""},
		{"", "", ""},
		{"/agent 在线有谁", "", ""},
		{"帮我查天气", "", ""},
		{"/unknown", "", ""},
	}
	for _, c := range cases {
		cmd, arg := MatchSystemCommand(c.in)
		if cmd != c.cmd || arg != c.arg {
			t.Errorf("Match(%q) = (%q,%q), want (%q,%q)", c.in, cmd, arg, c.cmd, c.arg)
		}
	}
}

func TestSysHelp(t *testing.T) {
	store := testSysStore(t)
	ctx := context.Background()
	qq := ExecSystemCommand(SysCtx{Ctx: ctx, Store: store, SessionID: "qq:c2c:U", Channel: "qq", IsAdmin: true}, "help", "")
	if !strings.Contains(qq, "/bind") || !strings.Contains(qq, "workspace") {
		t.Fatalf("qq help = %q", qq)
	}
	qqUser := ExecSystemCommand(SysCtx{Ctx: ctx, Store: store, SessionID: "qq:c2c:U", Channel: "qq"}, "help", "")
	if !strings.Contains(qqUser, "仅管理员可用") {
		t.Fatalf("qq user help = %q", qqUser)
	}
	mc := ExecSystemCommand(SysCtx{Ctx: ctx, Store: store, SessionID: "minecraft-main", Channel: "minecraft"}, "help", "")
	if !strings.Contains(mc, "@agent") || strings.Contains(mc, "/bind") {
		t.Fatalf("mc help = %q", mc)
	}
}

func TestSysMemory(t *testing.T) {
	store := testSysStore(t)
	ctx := context.Background()
	sctx := SysCtx{Ctx: ctx, Store: store, SessionID: "qq:c2c:U", Channel: "qq"}
	if got := ExecSystemCommand(sctx, "memory", ""); !strings.Contains(got, "0 条消息") {
		t.Fatalf("empty memory = %q", got)
	}
	if _, err := store.AppendMessage(ctx, storage.Message{SessionID: "qq:c2c:U", Channel: "qq", AuthorKind: "player", AuthorName: "U", Text: "hi", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if got := ExecSystemCommand(sctx, "memory", ""); !strings.Contains(got, "1 条消息") {
		t.Fatalf("memory = %q", got)
	}
	if got := ExecSystemCommand(sctx, "memory_summary", ""); !strings.Contains(got, "暂无摘要") {
		t.Fatalf("summary = %q", got)
	}
	if got := ExecSystemCommand(sctx, "memory_clear", ""); !strings.Contains(got, "已清空") {
		t.Fatalf("clear = %q", got)
	}
	if n, _ := store.CountMessages(ctx, "qq:c2c:U"); n != 0 {
		t.Fatalf("after clear n = %d", n)
	}
}

func TestSysBindMyID(t *testing.T) {
	store := testSysStore(t)
	ctx := context.Background()
	sctx := SysCtx{Ctx: ctx, Store: store, SessionID: "qq:c2c:U", Channel: "qq",
		QQIDs: []string{"U"}, Requester: "qq:U"}
	if got := ExecSystemCommand(sctx, "bind", "Steve"); !strings.Contains(got, "Steve") {
		t.Fatalf("bind = %q", got)
	}
	if got := ExecSystemCommand(sctx, "myid", ""); !strings.Contains(got, "Steve") {
		t.Fatalf("myid = %q", got)
	}
	if got := ExecSystemCommand(sctx, "unbind", ""); !strings.Contains(got, "已解绑") {
		t.Fatalf("unbind = %q", got)
	}
	if got := ExecSystemCommand(sctx, "myid", ""); !strings.Contains(got, "未绑定") {
		t.Fatalf("myid after unbind = %q", got)
	}
	// MC 通道 bind 直接拒绝。
	mc := SysCtx{Ctx: ctx, Store: store, SessionID: "minecraft-main", Channel: "minecraft", Requester: "Steve"}
	if got := ExecSystemCommand(mc, "bind", "Alex"); !strings.Contains(got, "不用绑定") {
		t.Fatalf("mc bind = %q", got)
	}
}

func TestSysStatus(t *testing.T) {
	store := testSysStore(t)
	ctx := context.Background()
	sctx := SysCtx{
		Ctx: ctx, Store: store, SessionID: "qq:c2c:U", Channel: "qq",
		Model: "test-model",
		MCStatus: func(ctx context.Context) (string, error) {
			return `{"tps1m":20}`, nil
		},
		QQStatus: func() string { return "已连接" },
	}
	got := ExecSystemCommand(sctx, "status", "")
	if !strings.Contains(got, "test-model") || !strings.Contains(got, "已连接") || !strings.Contains(got, "tps1m") {
		t.Fatalf("status = %q", got)
	}
}
