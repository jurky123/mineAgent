package tools

import (
	"testing"
	"time"

	"mineagent/internal/protocol"
)

func TestApprovalsLifecycle(t *testing.T) {
	ap := NewApprovals(&fakeSender{}, time.Minute, testLogger(t))
	info := ap.Create(ApprovalInfo{Tool: "minecraft_give", Requester: "Steve", Prompt: "给物品"})
	if info.ApprovalID == "" {
		t.Fatal("empty approval id")
	}
	if ap.Pending() != 1 {
		t.Fatalf("pending = %d", ap.Pending())
	}
	ap.Notify(info)
	ap.HandleResult(protocol.ApprovalResult{ApprovalID: info.ApprovalID, Approved: true, Operator: "jzk"})

	select {
	case out := <-ap.Outcomes():
		if !out.Decision.Approved || out.Decision.Operator != "jzk" {
			t.Fatalf("outcome = %+v", out.Decision)
		}
	case <-time.After(time.Second):
		t.Fatal("no outcome")
	}
	if ap.Pending() != 0 {
		t.Fatalf("pending = %d", ap.Pending())
	}
}

func TestApprovalsTimeout(t *testing.T) {
	ap := NewApprovals(&fakeSender{}, 30*time.Millisecond, testLogger(t))
	info := ap.Create(ApprovalInfo{Tool: "minecraft_teleport", Requester: "Steve"})
	ap.Notify(info)

	select {
	case out := <-ap.Outcomes():
		if out.Decision.Approved {
			t.Fatal("timeout should not be approved")
		}
		if out.Decision.Reason != "审批超时" {
			t.Fatalf("reason = %q", out.Decision.Reason)
		}
	case <-time.After(time.Second):
		t.Fatal("no timeout outcome")
	}
}

func TestApprovalsUnknownResult(t *testing.T) {
	ap := NewApprovals(&fakeSender{}, time.Minute, testLogger(t))
	ap.HandleResult(protocol.ApprovalResult{ApprovalID: "nope", Approved: true})
	select {
	case out := <-ap.Outcomes():
		t.Fatalf("unexpected outcome %+v", out)
	default:
	}
}
