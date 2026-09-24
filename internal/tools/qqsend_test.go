package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func qqSendCtx(target, session string) context.Context {
	ctx := context.Background()
	ctx = WithSession(ctx, session)
	ctx = WithReplyTarget(ctx, target)
	return ctx
}

func TestQQMarkdownBuildsInstruction(t *testing.T) {
	s := NewQQSend()
	ts := s.Tools()
	if len(ts) != 2 {
		t.Fatalf("tools = %d", len(ts))
	}
	md := ts[0].(tool.InvokableTool)
	out, err := md.InvokableRun(qqSendCtx("c2c:U1:MSG1", "qq:c2c:U1"), `{"content":"# 标题\n正文"}`)
	if err != nil {
		t.Fatal(err)
	}
	var cmd map[string]string
	if err := json.Unmarshal([]byte(out), &cmd); err != nil {
		t.Fatalf("out = %s", out)
	}
	if cmd[QQSEND_KEY] != "qq_markdown" {
		t.Fatalf("cmd = %v", cmd)
	}
	if cmd["target"] != "md:c2c:U1:MSG1" {
		t.Fatalf("target = %q", cmd["target"])
	}
	if cmd["text"] != "# 标题\n正文" {
		t.Fatalf("text = %q", cmd["text"])
	}
}

func TestQQMarkdownRejects(t *testing.T) {
	s := NewQQSend()
	md := s.Tools()[0].(tool.InvokableTool)
	// 无回执目标。
	if out, _ := md.InvokableRun(context.Background(), `{"content":"hi"}`); !strings.Contains(out, "error") {
		t.Fatalf("no target should fail: %s", out)
	}
	// 空内容。
	ctx := qqSendCtx("c2c:U1:MSG1", "qq:c2c:U1")
	if out, _ := md.InvokableRun(ctx, `{"content":"  "}`); !strings.Contains(out, "error") {
		t.Fatalf("empty should fail: %s", out)
	}
	// 非法 target。
	ctxBad := qqSendCtx("bad", "qq:c2c:U1")
	if out, _ := md.InvokableRun(ctxBad, `{"content":"hi"}`); !strings.Contains(out, "error") {
		t.Fatalf("bad target should fail: %s", out)
	}
}

func TestQQImageBuildsInstruction(t *testing.T) {
	s := NewQQSend()
	img := s.Tools()[1].(tool.InvokableTool)
	ctx := qqSendCtx("group:G1:MSG2", "qq:group:G1")
	out, err := img.InvokableRun(ctx, `{"path":"plot.png","caption":"今日统计"}`)
	if err != nil {
		t.Fatal(err)
	}
	var cmd map[string]string
	if err := json.Unmarshal([]byte(out), &cmd); err != nil {
		t.Fatalf("out = %s", out)
	}
	if cmd["target"] != "img:group:G1:MSG2:plot.png" {
		t.Fatalf("target = %q", cmd["target"])
	}
	if cmd["text"] != "今日统计" {
		t.Fatalf("caption = %q", cmd["text"])
	}
	if out, _ := img.InvokableRun(ctx, `{"path":""}`); !strings.Contains(out, "error") {
		t.Fatalf("empty path should fail: %s", out)
	}
}
