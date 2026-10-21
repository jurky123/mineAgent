package body

import "testing"

func TestMovementModeRemainsScopedToGoto(t *testing.T) {
	for _, mode := range []string{"ground", "swim"} {
		a, err := parseArgs("body_goto", `{"x":2,"z":3,"movement":"`+mode+`"}`)
		if err != nil || a.Movement != mode {
			t.Fatalf("valid mode rejected: %v", err)
		}
	}
	for _, input := range []struct{ tool, args string }{
		{"body_goto", `{"x":2,"z":3,"movement":"fly"}`},
		{"body_command", `{"command":"help","movement":"swim"}`},
		{"body_observe", `{"movement":"swim"}`},
	} {
		if _, err := parseArgs(input.tool, input.args); err == nil {
			t.Fatalf("accepted unsupported mode: %+v", input)
		}
	}
}
