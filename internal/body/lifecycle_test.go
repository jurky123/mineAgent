package body

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"mineagent/internal/protocol"
)

func invokeBody(t *testing.T, s *Service, p *Principal, name string) protocol.BodyResult {
	t.Helper()
	for _, base := range s.Tools() {
		tl := base.(*bodyTool)
		if tl.name != name {
			continue
		}
		raw, err := tl.InvokableRun(WithPrincipal(context.Background(), p), "{}")
		if err != nil {
			t.Fatal(err)
		}
		var r protocol.BodyResult
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	t.Fatal("missing tool", name)
	return protocol.BodyResult{}
}

func TestStatusRetainsPauseReasonFromBodyState(t *testing.T) {
	s, peer, p, _ := setup(t)
	st := s.state
	st.Seq++
	st.Paused, st.PauseReason = true, "forced_move"
	s.Handle(peer, envelope(protocol.TypeBodyState, st))
	r := invokeBody(t, s, p, "body_status")
	var health map[string]any
	if err := json.Unmarshal(r.Data, &health); err != nil {
		t.Fatal(err)
	}
	if !r.OK || health["paused"] != true || health["pauseReason"] != "forced_move" || health["controlConnected"] != true {
		t.Fatalf("pause cause lost in status: %+v %v", r, health)
	}
}

func TestPausedTaskFollowsOnlyItsInitialResume(t *testing.T) {
	s, peer, _, _ := setup(t)
	s.mu.Lock()
	s.paused = true
	s.mu.Unlock()
	p := s.Principal(1, 1, "admin")
	old := p.Epoch
	if !p.Deadline.IsZero() {
		t.Fatal("default task still has a deadline")
	}
	if _, err := s.Control(context.Background(), "resume", p); err != nil {
		t.Fatal(err)
	}
	previous := peer.send
	peer.send = func(typ string, v any) error {
		if typ != protocol.TypeBodyCall {
			return previous(typ, v)
		}
		c := v.(protocol.BodyCall)
		s.Handle(peer, envelope(protocol.TypeBodyResult, protocol.BodyResult{CallID: c.CallID, ConnectionID: c.ConnectionID, ControlEpoch: c.ControlEpoch, OK: true, Outcome: "confirmed"}))
		return nil
	}
	for _, name := range []string{"body_observe", "body_inventory", "body_observe"} {
		if r := invokeBody(t, s, p, name); !r.OK {
			t.Fatalf("resume made read stale: %+v", r)
		}
	}
	if err := s.Check(context.Background(), p, false); err != nil {
		t.Fatal("initial resume invalidated an unused task", err)
	}
	if p.Epoch != old+1 {
		t.Fatal("task did not bind to resumed epoch")
	}
	if _, err := s.Control(context.Background(), "stop", p); err != nil {
		t.Fatal(err)
	}
	fresh := s.Principal(1, 1, "admin")
	if _, err := s.Control(context.Background(), "resume", fresh); err != nil {
		t.Fatal(err)
	}
	if s.Check(context.Background(), p, false) == nil {
		t.Fatal("stop/resume revived old action authority")
	}
	if r := invokeBody(t, s, p, "body_observe"); !r.OK {
		t.Fatal("read-only diagnostic unavailable after epoch change", r)
	}
}

func TestStatusWorksOfflineAndStaleHasRealConnectionMetadata(t *testing.T) {
	s, peer, p, _ := setup(t)
	if _, err := s.Control(context.Background(), "stop", p); err != nil {
		t.Fatal(err)
	}
	r := s.toolFailure("test", "stale", p)
	var health map[string]any
	if err := json.Unmarshal(r.Data, &health); err != nil {
		t.Fatal(err)
	}
	if r.ConnectionID == "" || r.ControlEpoch == 0 || health["controlConnected"] != true || health["phase"] != "backend_preflight" {
		t.Fatalf("local rejection misrepresents a disconnected bridge: %+v %v", r, health)
	}
	s.Detach(peer)
	r = invokeBody(t, s, p, "body_status")
	if !r.OK {
		t.Fatal("backend status requires a live game transport", r)
	}
	json.Unmarshal(r.Data, &health)
	if health["controlConnected"] != false {
		t.Fatal("offline connection reported online")
	}
	r = invokeBody(t, s, nil, "body_status")
	if r.Code != "not_operator" || r.ConnectionID != "" || len(r.Data) != 0 {
		t.Fatal("status leaks without authorization", r)
	}
}

func TestStopAndRevocationCancelTaskBetweenTools(t *testing.T) {
	for _, action := range []string{"stop", "revoke", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			s, peer, p, allowed := setup(t)
			ctx, cancel := p.RunContext(context.Background())
			defer cancel()
			switch action {
			case "stop":
				_, _ = s.Control(context.Background(), "stop", p)
			case "revoke":
				allowed.Store(false)
			case "disconnect":
				s.Detach(peer)
			}
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
				t.Fatal("task survives loss of authority")
			}
		})
	}
}

func TestResumeIsIdempotentWhileAlreadyRunning(t *testing.T) {
	s, _, p, _ := setup(t)
	old := p.Epoch
	ack, err := s.Control(context.Background(), "resume", p)
	if err != nil || ack.ControlEpoch != old {
		t.Fatal("repeat resume revoked a live task", ack, err)
	}
}

func TestUnlimitedTaskStillBoundsPreparedActions(t *testing.T) {
	s, peer, p, _ := setup(t)
	var expires int64
	peer.send = func(typ string, v any) error {
		if typ == protocol.TypeBodyInspect {
			r := v.(protocol.BodyInspect)
			expires = r.ExpiresAt
			s.Handle(peer, envelope(protocol.TypeBodyInspectResult, protocol.BodyInspectResult{InspectID: r.InspectID, ConnectionID: r.ConnectionID, Code: "fixture"}))
		}
		return nil
	}
	_, _ = s.inspect(context.Background(), p, "body_command", protocol.BodyArgs{Command: "help"})
	remaining := time.Until(time.UnixMilli(expires))
	if remaining < 120*time.Second || remaining > 126*time.Second {
		t.Fatal("prepared action expiration is invalid", remaining)
	}
}

func TestRateReservationIsAtomicAndWaitingCanBeCancelled(t *testing.T) {
	s, _, p, _ := setup(t)
	s.cfg.Limits.BlocksPerMin = 1
	s.limits["blocks"] = []time.Time{time.Now()}
	if err := s.acquire("body_dig", p); err == nil {
		t.Fatal("quota was ignored")
	}
	if len(s.limits["all"]) != 0 || len(s.limits["actions"]) != 0 {
		t.Fatal("failed reservation consumed other quotas")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := s.waitAcquire(ctx, "body_dig", p); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("quota wait ignored cancellation", err)
	}
	s.limits["blocks"] = []time.Time{time.Now().Add(-time.Minute + 20*time.Millisecond)}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := s.waitAcquire(ctx2, "body_dig", p); err != nil {
		t.Fatal("task did not continue after quota refilled", err)
	}
	s.release()
}

func TestSuccessfulActionResetsConsecutiveFailures(t *testing.T) {
	s, peer, p, _ := setup(t)
	for _, success := range []bool{false, true, false, true, false} {
		peer.send = func(typ string, v any) error {
			if typ == protocol.TypeBodyCall {
				c := v.(protocol.BodyCall)
				s.Handle(peer, envelope(protocol.TypeBodyResult, protocol.BodyResult{CallID: c.CallID, ConnectionID: c.ConnectionID, ControlEpoch: c.ControlEpoch, OK: success, Outcome: "not_executed"}))
			}
			return nil
		}
		s.execute(context.Background(), p, "body_command", protocol.BodyArgs{Command: "help"}, ID(), nil, nil)
	}
	if err := s.Check(context.Background(), p, false); err != nil {
		t.Fatal("cumulative failures stopped a progressing task", err)
	}
}
