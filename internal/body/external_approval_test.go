package body

import (
	"context"
	"errors"
	"testing"

	"mineagent/internal/protocol"
	"mineagent/internal/tools"
)

func TestExternalApprovalReleasesSlotAndRechecksBodyOwner(t *testing.T) {
	for _, mode := range []string{"approve", "deny", "stale", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, peer, p, _ := setup(t)
			calls := 0
			peer.send = func(typ string, v any) error {
				switch typ {
				case protocol.TypeBodyInspect:
					req := v.(protocol.BodyInspect)
					target := [3]float64{1, 64, 1}
					sp := protocol.BodySpec{ID: "fixture", ConnectionID: req.ConnectionID, ControlEpoch: req.ControlEpoch, PolicyRevision: req.PolicyRevision, Tool: req.Tool, Args: req.Args, ExpiresAt: req.ExpiresAt, WorldID: "yard", Dimension: "minecraft:overworld", Pos: [3]float64{0, 64, 0}, Target: &target, Block: "oak_door", Hazards: []string{"fixture"}}
					s.Handle(peer, envelope(protocol.TypeBodyInspectResult, protocol.BodyInspectResult{InspectID: req.InspectID, ConnectionID: req.ConnectionID, OK: true, Spec: &sp}))
				case protocol.TypeBodyCall:
					calls++
					req := v.(protocol.BodyCall)
					if req.Grant == nil || req.Grant.ApprovalID == "" {
						t.Error("missing approval grant")
					}
					s.Handle(peer, envelope(protocol.TypeBodyResult, protocol.BodyResult{CallID: req.CallID, ConnectionID: req.ConnectionID, ControlEpoch: req.ControlEpoch, OK: true, Outcome: "confirmed"}))
				}
				return nil
			}
			ctx, cancel := context.WithCancel(WithPrincipal(context.Background(), p))
			defer cancel()
			ctx, decisions := tools.WithExternalApprovals(ctx)
			tl := &bodyTool{service: s, name: "body_interact"}
			raw := `{"x":1,"y":64,"z":1,"reason":"fixture approval"}`
			_, err := tl.InvokableRun(ctx, raw)
			var pending *tools.ExternalApprovalPending
			if !errors.As(err, &pending) {
				t.Fatalf("no external suspension: %v", err)
			}
			s.mu.Lock()
			busy := s.busy
			s.mu.Unlock()
			if busy {
				t.Fatal("tool held body slot while suspended")
			}
			s.approvals.Notify(pending.Info)
			switch mode {
			case "stale":
				s.mu.Lock()
				s.epoch++
				s.mu.Unlock()
				s.approvals.DecideBody(pending.Info.ApprovalID, true, "admin")
			case "cancel":
				cancel()
				s.approvals.Cancel(pending.Info.ApprovalID)
			default:
				s.approvals.DecideBody(pending.Info.ApprovalID, mode == "approve", "admin")
			}
			d := <-decisions
			if _, err := tl.InvokableRun(tools.WithApprovalDecision(ctx, &d), raw); err != nil {
				t.Fatal(err)
			}
			if mode == "approve" && calls != 1 {
				t.Fatalf("approved action calls=%d", calls)
			}
			if mode != "approve" && calls != 0 {
				t.Fatal("unapproved/stale action executed")
			}
			s.mu.Lock()
			waiting := len(s.waiting)
			s.mu.Unlock()
			if waiting != 0 || len(s.approvals.DrainDecided()) != 0 {
				t.Fatal("suspended approval leaked")
			}
		})
	}
}
