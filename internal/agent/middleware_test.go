package agent

import "testing"

func TestLooksLikeToolCall(t *testing.T) {
	for _, bad := range []string{
		"<｜｜DSML｜｜ calls>",
		`invoke name="workspace_exec"`,
		`parameter name="command"`,
		"xxx <｜ yyy",
		"aaa calls> bbb",
	} {
		if !looksLikeToolCall(bad) {
			t.Fatalf("%q should be tool call", bad)
		}
	}
	for _, ok := range []string{
		"对话摘要：用户问天气，助手查了 wttr.in",
		"保留发言人、请求事项和未完成的任务",
		"",
	} {
		if looksLikeToolCall(ok) {
			t.Fatalf("%q should not be tool call", ok)
		}
	}
}
