package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mineagent/internal/config"
	"mineagent/internal/protocol"
	"mineagent/internal/storage"
)

func testApprovalTool(t *testing.T, sender *fakeSender) (*approvalTool, *Approvals) {
	t.Helper()
	gw := NewGateway(sender, testLogger(t))
	gw.timeout = time.Second
	sender.gw = gw

	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	approvals := NewApprovals(sender, time.Minute, testLogger(t))
	all := Privileged(gw, approvals, store, config.DefaultTools(), testLogger(t))
	for _, tl := range all {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name == "minecraft_run_command" {
			return tl.(*approvalTool), approvals
		}
	}
	t.Fatal("run_command tool not found")
	return nil, nil
}

func answerCheck(sender *fakeSender, allowed bool, permission string) {
	sender.onCall = func(call protocol.ToolCall) {
		if call.Tool != internalCheckCommand {
			return
		}
		payload := map[string]any{"allowed": allowed, "permission": permission}
		raw, _ := json.Marshal(payload)
		sender.gw.HandleResult(protocol.ToolResult{CallID: call.CallID, OK: true, Data: raw})
	}
}

func TestCommandPermissionDeniedByRequester(t *testing.T) {
	sender := &fakeSender{}
	tl, approvals := testApprovalTool(t, sender)
	answerCheck(sender, false, "minecraft.command.gamemode")

	out, err := tl.InvokableRun(context.Background(), `{"command":"gamemode creative SmokeBot"}`)
	if err != nil {
		t.Fatalf("expected content error, got %v", err)
	}
	if !strings.Contains(out, "权限") {
		t.Fatalf("out = %s", out)
	}
	if approvals.Pending() != 0 {
		t.Fatalf("approval should not be created, pending=%d", approvals.Pending())
	}
}

func TestCommandPermissionAllowedCreatesInterrupt(t *testing.T) {
	sender := &fakeSender{}
	tl, approvals := testApprovalTool(t, sender)
	answerCheck(sender, true, "minecraft.command.time")

	out, err := tl.InvokableRun(context.Background(), `{"command":"time set day"}`)
	if out != "" {
		t.Fatalf("out = %q", out)
	}
	if err == nil {
		t.Fatal("expected interrupt error")
	}
	if approvals.Pending() != 1 {
		t.Fatalf("pending = %d", approvals.Pending())
	}
}
