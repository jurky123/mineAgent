package tools

import (
	"context"
	"strings"
	"testing"
)

func TestEffortCommandValidatesAndDoesNotInvokeModel(t *testing.T) {
	s := testSysStore(t)
	ctx := context.Background()
	key := "qq:c2c:fixture"
	if err := s.EnsureSession(ctx, key, 1); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ arg, want string }{{"低", "low"}, {"MEDIUM", "medium"}, {"high", "high"}, {"默认", ""}} {
		reply, err := EffortCommand(ctx, s, key, tc.arg, "high")
		if err != nil || !strings.Contains(reply, "下一条新请求") {
			t.Fatal(reply, err)
		}
		got, err := s.SessionEffort(ctx, key)
		if err != nil || got != tc.want {
			t.Fatal(tc.arg, got, err)
		}
	}
	for _, bad := range []string{"off", "xhigh", "high extra", "none"} {
		reply, err := EffortCommand(ctx, s, key, bad, "high")
		if err != nil || !strings.Contains(reply, "用法") {
			t.Fatal("invalid command", reply, err)
		}
	}
	reply, err := EffortCommand(ctx, s, key, "", "high")
	if err != nil || !strings.Contains(reply, "当前沿用 高") {
		t.Fatal("missing inherited value", reply, err)
	}
	if cmd, arg := MatchConversationCommand("/EFFORT 高"); cmd != "effort" || arg != "高" {
		t.Fatal("not a direct command", cmd, arg)
	}
}
