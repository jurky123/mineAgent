package tools

import (
	"testing"
	"time"

	"mineagent/internal/protocol"
)

func TestApprovalsLifecycle(t *testing.T) {
	ap := NewApprovals(&fakeSender{}, time.Minute, testLogger(t))
	info, err := ap.Create(ApprovalInfo{Tool: "minecraft_give", Requester: "Steve", Prompt: "给物品"})
	if err != nil {
		t.Fatal(err)
	}
	if info.ApprovalID == "" {
		t.Fatal("empty approval id")
	}
	if ap.Pending() != 1 {
		t.Fatalf("pending = %d", ap.Pending())
	}
	ap.Notify(info)
	ap.HandleResult(protocol.ApprovalResult{ApprovalID: info.ApprovalID, Approved: true, Operator: "jzk"})

	select {
	case <-ap.Decided():
	default:
		t.Fatal("expected decided signal")
	}
	outcomes := ap.DrainDecided()
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %d", len(outcomes))
	}
	if !outcomes[0].Decision.Approved || outcomes[0].Decision.Operator != "jzk" {
		t.Fatalf("outcome = %+v", outcomes[0].Decision)
	}
	if len(ap.DrainDecided()) != 0 {
		t.Fatal("drain should clear decided outcomes")
	}
	if ap.Pending() != 0 {
		t.Fatalf("pending = %d", ap.Pending())
	}
}

func TestApprovalsTimeout(t *testing.T) {
	ap := NewApprovals(&fakeSender{}, 30*time.Millisecond, testLogger(t))
	info, err := ap.Create(ApprovalInfo{Tool: "minecraft_teleport", Requester: "Steve"})
	if err != nil {
		t.Fatal(err)
	}
	ap.Notify(info)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		outcomes := ap.DrainDecided()
		if len(outcomes) > 0 {
			if outcomes[0].Decision.Approved {
				t.Fatal("timeout should not be approved")
			}
			if outcomes[0].Decision.Reason != "审批超时" {
				t.Fatalf("reason = %q", outcomes[0].Decision.Reason)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no timeout outcome")
}

func TestApprovalsUnknownResult(t *testing.T) {
	ap := NewApprovals(&fakeSender{}, time.Minute, testLogger(t))
	ap.HandleResult(protocol.ApprovalResult{ApprovalID: "nope", Approved: true})
	if len(ap.DrainDecided()) != 0 {
		t.Fatal("unknown result must not produce outcome")
	}
	select {
	case <-ap.Decided():
		t.Fatal("unknown result must not signal")
	default:
	}
}

func TestApprovalsPendingCap(t *testing.T) {
	ap := NewApprovals(&fakeSender{}, time.Minute, testLogger(t))
	ap.maxPending = 2
	if _, err := ap.Create(ApprovalInfo{Tool: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ap.Create(ApprovalInfo{Tool: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ap.Create(ApprovalInfo{Tool: "c"}); err == nil {
		t.Fatal("expected pending cap error")
	}
}
