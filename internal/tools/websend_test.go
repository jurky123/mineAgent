package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

// 企微/网页目标只有两段（c2c:<id>），不能再按 QQ 的三段式切。
// 之前 web_file 就是在这里报"回执目标非法"（wecom_markdown/wecom_image 同样中招）。
func TestWebFileBuildsInstruction(t *testing.T) {
	s := NewWebSend()
	ts := s.Tools()
	if len(ts) != 1 {
		t.Fatalf("tools = %d", len(ts))
	}
	f := ts[0].(tool.InvokableTool)
	out, err := f.InvokableRun(qqSendCtx("c2c:jzk", "web:c2c:jzk"), `{"path":"report.pdf","caption":"报表"}`)
	if err != nil {
		t.Fatal(err)
	}
	var cmd map[string]string
	if err := json.Unmarshal([]byte(out), &cmd); err != nil {
		t.Fatalf("out = %s", out)
	}
	if cmd[WEBSEND_KEY] != "web_file" {
		t.Fatalf("cmd = %v", cmd)
	}
	if cmd["target"] != "file:c2c:jzk:report.pdf" {
		t.Fatalf("target = %q", cmd["target"])
	}
	if cmd["text"] != "报表" {
		t.Fatalf("caption = %q", cmd["text"])
	}
}

func TestWebFileRejects(t *testing.T) {
	s := NewWebSend()
	f := s.Tools()[0].(tool.InvokableTool)
	if out, _ := f.InvokableRun(qqSendCtx("bad", "web:c2c:jzk"), `{"path":"a.png"}`); !strings.Contains(out, "error") {
		t.Fatalf("bad target should fail: %s", out)
	}
	if out, _ := f.InvokableRun(qqSendCtx("c2c:jzk", "web:c2c:jzk"), `{"path":"  "}`); !strings.Contains(out, "error") {
		t.Fatalf("empty path should fail: %s", out)
	}
	if out, _ := f.InvokableRun(qqSendCtx("group:G1", "web:c2c:jzk"), `{"path":"a.png"}`); !strings.Contains(out, "error") {
		t.Fatalf("group target should fail: %s", out)
	}
}

func TestWeComMarkdownTwoPartTarget(t *testing.T) {
	s := NewWeComSend()
	md := s.Tools()[0].(tool.InvokableTool)
	out, err := md.InvokableRun(qqSendCtx("c2c:user1", "wecom:c2c:user1"), `{"content":"# 标题"}`)
	if err != nil {
		t.Fatal(err)
	}
	var cmd map[string]string
	if err := json.Unmarshal([]byte(out), &cmd); err != nil {
		t.Fatalf("out = %s", out)
	}
	if cmd["target"] != "md:c2c:user1" {
		t.Fatalf("target = %q", cmd["target"])
	}
}

func TestSplitIMTarget(t *testing.T) {
	for _, tc := range []struct {
		in   string
		ok   bool
		want string
	}{
		{"c2c:jzk", true, "c2c:jzk"},
		{"group:G1", true, "group:G1"},
		{"c2c:U1:MSG", true, "c2c:U1"},
		{"c2c:", false, ""},
		{"bad", false, ""},
		{"other:x", false, ""},
	} {
		kind, id, ok := splitIMTarget(tc.in)
		if ok != tc.ok {
			t.Fatalf("splitIMTarget(%q) ok=%v want %v", tc.in, ok, tc.ok)
		}
		if ok && kind+":"+id != tc.want {
			t.Fatalf("splitIMTarget(%q) = %q", tc.in, kind+":"+id)
		}
	}
}
