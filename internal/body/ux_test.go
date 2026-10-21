package body

import (
	"context"
	"mineagent/internal/protocol"
	"testing"
	"time"
)

func TestConsecutiveReadBudgetAndActionReset(t *testing.T) {
	s, peer, p, _ := setup(t)
	previous := peer.send
	peer.send = func(typ string, v any) error {
		if typ != protocol.TypeBodyCall {
			return previous(typ, v)
		}
		c := v.(protocol.BodyCall)
		s.Handle(peer, envelope(protocol.TypeBodyResult, protocol.BodyResult{CallID: c.CallID, ConnectionID: c.ConnectionID, ControlEpoch: c.ControlEpoch, OK: true, Outcome: "confirmed"}))
		return nil
	}
	for i := 0; i < 3; i++ {
		if r := invokeBody(t, s, p, "body_status"); !r.OK {
			t.Fatal(r)
		}
	}
	if r := invokeBody(t, s, p, "body_status"); r.Code != "observation_budget" {
		t.Fatal(r)
	}
	r := s.execute(context.Background(), p, "body_command", protocol.BodyArgs{Command: "help"}, ID(), nil, nil)
	if !r.OK {
		t.Fatal(r)
	}
	if r := invokeBody(t, s, p, "body_status"); !r.OK {
		t.Fatal("action did not reset budget", r)
	}
}

func TestCancelActionPreservesEpochAndUnpausedState(t *testing.T) {
	s, peer, p, _ := setup(t)
	epoch := p.Epoch
	s.activeCallID = "cancel-target"
	previous := peer.send
	peer.send = func(typ string, v any) error {
		if typ != protocol.TypeBodyControl {
			return previous(typ, v)
		}
		c := v.(protocol.BodyControl)
		if c.Action != "cancel" || c.CallID != "cancel-target" || c.ControlEpoch != epoch {
			t.Errorf("unsafe cancel: %+v", c)
		}
		s.Handle(peer, envelope(protocol.TypeBodyControlAck, protocol.BodyAck{ID: c.ID, ConnectionID: c.ConnectionID, ControlEpoch: c.ControlEpoch, Paused: false}))
		return nil
	}
	r := invokeBody(t, s, p, "body_stop")
	if !r.OK || s.paused || s.epoch != epoch {
		t.Fatalf("cancel latched pause/revoked run: %+v", r)
	}
	if err := s.Check(context.Background(), p, false); err != nil {
		t.Fatal(err)
	}
}

func TestPausedExistingTaskContinuesWithoutNewPrincipal(t *testing.T) {
	for _, reason := range []string{"pause", "forced_move"} {
		t.Run(reason, func(t *testing.T) {
			s, peer, p, _ := setup(t)
			ctx, cancel := p.RunContext(context.Background())
			defer cancel()
			if reason == "pause" {
				if _, err := s.Control(ctx, "pause", p); err != nil {
					t.Fatal(err)
				}
			} else {
				st := s.state
				st.Seq++
				st.Paused = true
				st.PauseReason = reason
				s.Handle(peer, envelope(protocol.TypeBodyState, st))
			}
			waiting := make(chan error, 1)
			go func() { waiting <- s.WaitForResume(ctx, p) }()
			select {
			case err := <-waiting:
				t.Fatal("pause did not wait", err)
			case <-time.After(120 * time.Millisecond):
			}
			st := s.state
			st.Paused = false
			st.PauseReason = "" // fake transport's next state
			s.mu.Lock()
			s.state = st
			s.mu.Unlock()
			fresh := s.Principal(1, 1, "admin")
			if _, err := s.Control(ctx, "resume", fresh); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-waiting:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("resume left original task parked")
			}
			if err := s.Check(ctx, p, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPausedTaskStillRevokesAuthorization(t *testing.T) {
	s, peer, p, allowed := setup(t)
	st := s.state
	st.Seq++
	st.Paused = true
	st.PauseReason = "forced_move"
	s.Handle(peer, envelope(protocol.TypeBodyState, st))
	allowed.Store(false)
	if err := s.WaitForResume(context.Background(), p); err == nil || err.Error() != "not_operator" {
		t.Fatal("paused run kept revoked authority", err)
	}
}

func TestUnknownSideEffectsCannotResumeAnAlreadyPausedTask(t *testing.T) {
	s, peer, p, _ := setup(t)
	st := s.state
	st.Seq++
	st.Paused = true
	st.PauseReason = "forced_move"
	s.Handle(peer, envelope(protocol.TypeBodyState, st))
	if err := s.Check(context.Background(), p, false); err != nil {
		t.Fatal(err)
	}
	s.pauseUnknown()
	if _, err := s.Control(context.Background(), "resume", s.Principal(1, 1, "admin")); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(context.Background(), p, false); err == nil || err.Error() != "stale" {
		t.Fatal("unknown effects revived original paused authority", err)
	}
}
