package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mineagent/internal/protocol"
	"mineagent/internal/storage"
)

func testTool(t *testing.T, sender *fakeSender, name string) (*approvalTool, *Approvals) {
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
	all := Privileged(gw, approvals, store, testLogger(t))
	for _, tl := range all {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name == name {
			return tl.(*approvalTool), approvals
		}
	}
	t.Fatalf("%s tool not found", name)
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

func answerPermission(sender *fakeSender, allowed bool, reason string) {
	sender.onCall = func(call protocol.ToolCall) {
		if call.Tool != internalCheckPermission {
			return
		}
		payload := map[string]any{"allowed": allowed, "reason": reason}
		raw, _ := json.Marshal(payload)
		sender.gw.HandleResult(protocol.ToolResult{CallID: call.CallID, OK: true, Data: raw})
	}
}

func TestCommandPermissionDeniedByRequester(t *testing.T) {
	sender := &fakeSender{}
	tl, approvals := testTool(t, sender, "minecraft_run_command")
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
	tl, approvals := testTool(t, sender, "minecraft_run_command")
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

func TestGivePermissionDenied(t *testing.T) {
	sender := &fakeSender{}
	tl, approvals := testTool(t, sender, "minecraft_give")
	answerPermission(sender, false, "")

	out, err := tl.InvokableRun(context.Background(), `{"player":"Steve","item":"diamond","count":1}`)
	if err != nil {
		t.Fatalf("expected content error, got %v", err)
	}
	if !strings.Contains(out, "mineagent.give") {
		t.Fatalf("out = %s", out)
	}
	if approvals.Pending() != 0 {
		t.Fatalf("approval should not be created, pending=%d", approvals.Pending())
	}
}

func TestTeleportPermissionAllowedCreatesInterrupt(t *testing.T) {
	sender := &fakeSender{}
	tl, approvals := testTool(t, sender, "minecraft_teleport")
	answerPermission(sender, true, "")

	out, err := tl.InvokableRun(context.Background(), `{"player":"Steve","target":"Alex"}`)
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

func TestTeleportOfflineRequester(t *testing.T) {
	sender := &fakeSender{}
	tl, approvals := testTool(t, sender, "minecraft_teleport")
	answerPermission(sender, false, "请求者不在线")

	out, err := tl.InvokableRun(context.Background(), `{"player":"Steve","target":"Alex"}`)
	if err != nil {
		t.Fatalf("expected content error, got %v", err)
	}
	if !strings.Contains(out, "不在线") {
		t.Fatalf("out = %s", out)
	}
	if approvals.Pending() != 0 {
		t.Fatalf("approval should not be created, pending=%d", approvals.Pending())
	}
}

func TestPermissionCheckFailureDenies(t *testing.T) {
	sender := &fakeSender{}
	tl, approvals := testTool(t, sender, "minecraft_give")
	sender.gw.timeout = 50 * time.Millisecond

	out, err := tl.InvokableRun(context.Background(), `{"player":"Steve","item":"diamond","count":1}`)
	if err != nil {
		t.Fatalf("expected content error, got %v", err)
	}
	if !strings.Contains(out, "权限校验失败") {
		t.Fatalf("out = %s", out)
	}
	if approvals.Pending() != 0 {
		t.Fatalf("approval should not be created when check fails, pending=%d", approvals.Pending())
	}
}
