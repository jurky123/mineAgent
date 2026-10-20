package body

import (
	"context"
	"encoding/json"

	"mineagent/internal/protocol"
	"strings"
	"testing"
)

func TestPartialNavigationProgressUsesRealDisplacementTowardTarget(t *testing.T) {
	target := [3]float64{10, 64, 0}
	sp := &protocol.BodySpec{Tool: "body_goto", Pos: [3]float64{0, 64, 0}, Target: &target}
	for _, tc := range []struct {
		name, code, outcome, data string
		progress                  bool
	}{
		{"forward partial", "path_not_found", "confirmed", `{"partial":true,"position":[2,64,0]}`, true},
		{"search budget progress", "path_search_timeout", "confirmed", `{"partial":true,"position":[2,64,0]}`, true},
		{"legacy partial", "failed", "confirmed", `{"partial":true,"position":[2,64,0]}`, true},
		{"stationary", "path_not_found", "confirmed", `{"partial":true,"position":[0,64,0],"progressDistance":100}`, false},
		{"sideways", "path_not_found", "confirmed", `{"partial":true,"position":[0,64,3]}`, false},
		{"tiny drift", "path_not_found", "confirmed", `{"partial":true,"position":[0.1,64,0]}`, false},
		{"cancelled", "path_stopped", "confirmed", `{"partial":true,"position":[2,64,0]}`, false},
		{"unconfirmed", "timeout", "unknown", `{"partial":true,"position":[2,64,0]}`, false},
		{"missing position", "path_not_found", "confirmed", `{"partial":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := protocol.BodyResult{Code: tc.code, Outcome: tc.outcome, Data: json.RawMessage(tc.data)}
			if got := navigationProgress(r, sp); got != tc.progress {
				t.Fatal(got, tc.progress)
			}
		})
	}
}

func TestNavigationStopsAfterThreeNoProgressResultsButNotPartialAdvances(t *testing.T) {
	s, peer, p, _ := setup(t)
	target := [3]float64{10, 64, 0}
	sp := &protocol.BodySpec{Tool: "body_goto", Pos: [3]float64{0, 64, 0}, Target: &target}
	var pos [3]float64
	peer.send = func(typ string, v any) error {
		if typ == protocol.TypeBodyCall {
			c := v.(protocol.BodyCall)
			data, _ := json.Marshal(map[string]any{"partial": true, "position": pos})
			s.Handle(peer, envelope(protocol.TypeBodyResult, protocol.BodyResult{CallID: c.CallID, ConnectionID: c.ConnectionID, ControlEpoch: c.ControlEpoch, Code: "path_not_found", Outcome: "confirmed", Data: data}))
		}
		return nil
	}
	for i := 0; i < 3; i++ {
		for _, x := range []float64{0, 0, 2} {
			pos = [3]float64{x, 64, 0}
			s.execute(context.Background(), p, "body_goto", protocol.BodyArgs{}, ID(), sp, nil)
		}
		if err := s.Check(context.Background(), p, false); err != nil {
			t.Fatal("progressing task was cancelled", err)
		}
	}
	for i := 0; i < 3; i++ {
		pos = [3]float64{0, 64, 0}
		s.execute(context.Background(), p, "body_goto", protocol.BodyArgs{}, ID(), sp, nil)
	}
	if err := s.Check(context.Background(), p, false); err == nil || err.Error() != "run_failed" {
		t.Fatal("stuck task did not stop", err)
	}
	if !strings.Contains(p.FailureSummary(), "body_goto（path_not_found）") {
		t.Fatal("stop hid the specific cause", p.FailureSummary())
	}
}
