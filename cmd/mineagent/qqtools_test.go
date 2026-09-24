package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

func TestSplitSendInner(t *testing.T) {
	base, path := splitSendInner("c2c:U1:MSG1")
	if base != "c2c:U1:MSG1" || path != "" {
		t.Fatalf("got %q %q", base, path)
	}
	base, path = splitSendInner("group:G1:MSG2:plot.png")
	if base != "group:G1:MSG2" || path != "plot.png" {
		t.Fatalf("got %q %q", base, path)
	}
	if base, _ := splitSendInner("bad"); base != "" {
		t.Fatal("bad should fail")
	}
	if base, _ := splitSendInner("c2c:U1"); base != "" {
		t.Fatal("short should fail")
	}
}

func TestCheckWorkspaceRelPath(t *testing.T) {
	for _, bad := range []string{"", "/etc/passwd", "../x.png", ".."} {
		if err := checkWorkspaceRelPath(bad); err == nil {
			t.Fatalf("%q should fail", bad)
		}
	}
	if err := checkWorkspaceRelPath("sub/plot.png"); err != nil {
		t.Fatalf("normal: %v", err)
	}
}

func TestSendQQTargetBinding(t *testing.T) {
	// gate 无 sender/sessions 时，target 校验先行：跨会话伪造必须被拦。
	g := &qqToolGate{}
	ctx := tools.WithQQReplyTarget(context.Background(), "c2c:U1:MSG1")
	ctx = tools.WithSession(ctx, "qq:c2c:U1")
	// md 正常 target 但 sender 未就绪 -> 报未就绪（说明校验通过）。
	if err := g.sendQQ(ctx, "md:c2c:U1:MSG1", "hi"); err == nil || !strings.Contains(err.Error(), "未就绪") {
		t.Fatalf("want 未就绪, got %v", err)
	}
	// 跨会话伪造 -> 拒绝。
	if err := g.sendQQ(ctx, "md:c2c:OTHER:MSG9", "hi"); err == nil || !strings.Contains(err.Error(), "当前会话") {
		t.Fatalf("want 当前会话, got %v", err)
	}
	// img 非法路径 -> 拒绝。
	if err := g.sendQQ(ctx, "img:c2c:U1:MSG1:../x.png", ""); err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("want workspace, got %v", err)
	}
	// sender 就绪后正常发送。
	var gotTarget, gotText string
	g.sender = func(ctx context.Context, sess *session.Session, target, text string) error {
		gotTarget, gotText = target, text
		return nil
	}
	g.sessions = func(ctx context.Context, key string) (*session.Session, error) {
		if key != "qq:c2c:U1" {
			t.Fatalf("sessionKey = %q", key)
		}
		return &session.Session{}, nil
	}
	if err := g.sendQQ(ctx, "md:c2c:U1:MSG1", "hi"); err != nil {
		t.Fatal(err)
	}
	if gotTarget != "md:c2c:U1:MSG1" || gotText != "hi" {
		t.Fatalf("got %q %q", gotTarget, gotText)
	}
	_ = json.Marshal
	_ = storage.KindMarkdown
}
