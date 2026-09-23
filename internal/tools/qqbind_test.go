package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"mineagent/internal/protocol"
	"mineagent/internal/storage"
)

// QQ 外部身份（qq:<openid>）调高权限工具：跳过"本人权限预检"，直接创建审批
// （转游戏内管理员批准）。回归锁：以后改 privileged.go 不能把 QQ 路径卡死。
func TestQQExternalSkipsPermissionPrecheck(t *testing.T) {
	sender := &fakeSender{}
	tl, approvals := testTool(t, sender, "minecraft_teleport")
	// 注意：不设置 answerPermission（默认 fakeSender 不回包）。
	// MC 路径此时会卡在"权限校验失败"；QQ 路径必须直接建审批。
	ctx := WithRequester(context.Background(), "qq:UNION123")
	ctx = WithSession(ctx, "qq:c2c:U1")

	out, err := tl.InvokableRun(ctx, `{"player":"Steve","target":"Alex"}`)
	if out != "" {
		t.Fatalf("out = %q, want empty (interrupt)", out)
	}
	if err == nil {
		t.Fatal("expected interrupt error")
	}
	if approvals.Pending() != 1 {
		t.Fatalf("pending = %d, want 1", approvals.Pending())
	}
}

// 同一 QQ 用户挂起超限：Create 直接拒绝，不建新审批。
func TestQQMaxPendingPerRequester(t *testing.T) {
	ap := NewApprovals(&fakeSender{}, time.Minute, testLogger(t))
	ap.SetMaxPerRequester(1)
	if _, err := ap.Create(ApprovalInfo{Tool: "minecraft_give", Requester: "qq:U1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ap.Create(ApprovalInfo{Tool: "minecraft_give", Requester: "qq:U1"}); err == nil {
		t.Fatal("second pending from same requester should be rejected")
	}
	// 别人不受影响。
	if _, err := ap.Create(ApprovalInfo{Tool: "minecraft_give", Requester: "qq:U2"}); err != nil {
		t.Fatalf("other requester should pass: %v", err)
	}
	// MC 玩家也受同一计数器约束（main.go 里共用一个 approvals 实例），但默认
	// maxPerRequester=0 即不限；这里设了 1，所以 Steve 第二个也会被限——
	// 这是符合预期的：限的是"同一请求者"，不分通道。
	if n := ap.PendingFor("qq:U1"); n != 1 {
		t.Fatalf("PendingFor = %d, want 1", n)
	}
}

// Gateway.Call 把 qq: 前缀翻译成绑定的 MC 名再发给插件；MC 路径保持原样。
func TestGatewayTranslatesQQRequester(t *testing.T) {
	sender := &fakeSender{}
	gw := NewGateway(sender, testLogger(t))
	gw.timeout = time.Second
	sender.gw = gw

	got := make(chan protocol.ToolCall, 2)
	sender.onCall = func(call protocol.ToolCall) {
		got <- call
		// 异步回成功，避免 Call 阻塞。
		go gw.HandleResult(protocol.ToolResult{CallID: call.CallID, OK: true, Data: json.RawMessage(`{}`)})
	}

	// QQ：requester=qq:UNION1 + mcRequester=Steve，发给插件的必须是 Steve。
	ctx := WithRequester(context.Background(), "qq:UNION1")
	ctx = WithMCRequester(ctx, "Steve")
	if _, err := gw.Call(ctx, "minecraft_list_players", nil); err != nil {
		t.Fatal(err)
	}
	call := <-got
	if call.Requester != "Steve" {
		t.Fatalf("qq requester = %q, want Steve", call.Requester)
	}

	// MC：requester=Steve，原样透传。
	ctx2 := WithRequester(context.Background(), "Steve")
	if _, err := gw.Call(ctx2, "minecraft_list_players", nil); err != nil {
		t.Fatal(err)
	}
	call2 := <-got
	if call2.Requester != "Steve" {
		t.Fatalf("mc requester = %q, want Steve", call2.Requester)
	}

	// QQ 未绑定：mcRequester 为空，发给插件的就是空，插件走外部审批路径。
	ctx3 := WithRequester(context.Background(), "qq:UNION9")
	if _, err := gw.Call(ctx3, "minecraft_list_players", nil); err != nil {
		t.Fatal(err)
	}
	call3 := <-got
	if call3.Requester != "" {
		t.Fatalf("unbound qq requester = %q, want empty", call3.Requester)
	}
}

func TestQQBindUnbind(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	b := NewQQBind(store, testLogger(t))
	ts := b.Tools()
	if len(ts) != 2 {
		t.Fatalf("tools = %d", len(ts))
	}
	ctx := WithQQIdentity(context.Background(), []string{"UN1", "U1"})

	bind, ok := ts[0].(tool.InvokableTool)
	if !ok {
		t.Fatal("qq_bind not invokable")
	}
	out, err := bind.InvokableRun(ctx, `{"player":"Steve"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Steve") {
		t.Fatalf("bind out = %s", out)
	}
	// 两个 openid 都能查到绑定。
	for _, id := range []string{"UN1", "U1"} {
		name, err := store.LinkedMC(ctx, "qq", id)
		if err != nil {
			t.Fatal(err)
		}
		if name != "Steve" {
			t.Fatalf("LinkedMC(%s) = %q", id, name)
		}
	}

	unbind, ok := ts[1].(tool.InvokableTool)
	if !ok {
		t.Fatal("qq_unbind not invokable")
	}
	if _, err := unbind.InvokableRun(ctx, `{}`); err != nil {
		t.Fatal(err)
	}
	if name, _ := store.LinkedMC(ctx, "qq", "UN1"); name != "" {
		t.Fatalf("after unbind = %q", name)
	}
}

func TestQQBindRejectsBadName(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	b := NewQQBind(store, testLogger(t))
	ctx := WithQQIdentity(context.Background(), []string{"UN1"})
	bind := b.Tools()[0].(tool.InvokableTool)
	for _, args := range []string{`{"player":""}`, `{"player":"a b"}`, `{}`} {
		out, err := bind.InvokableRun(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "error") {
			t.Fatalf("args %s should fail, got %s", args, out)
		}
	}
}
