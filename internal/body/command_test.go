package body

import (
	"encoding/json"
	"testing"
)

func TestCommandParsingPreservesPlayerAndWorldEditSyntax(t *testing.T) {
	for _, command := range []string{"give MineAgentBot stone 1", "/give MineAgentBot stone 1", "//pos1 10,64,10", "//set stone", "/worldedit:/size"} {
		raw, _ := json.Marshal(map[string]string{"command": "  " + command + "  "})
		a, err := parseArgs("body_command", string(raw))
		if err != nil || a.Command != command {
			t.Fatalf("%q changed before transport: %q, %v", command, a.Command, err)
		}
		// Revalidation must not remove another slash from an immutable action.
		raw, _ = json.Marshal(a)
		again, err := parseArgs("body_command", string(raw))
		if err != nil || again.Command != command {
			t.Fatalf("revalidation changed %q: %q, %v", command, again.Command, err)
		}
	}
	for _, command := range []string{"/agent test", "//ma test", "/minecraft:AI test", "mineagent:/agent test", "/", "//", "/   "} {
		raw, _ := json.Marshal(map[string]string{"command": command})
		if _, err := parseArgs("body_command", string(raw)); err == nil {
			t.Fatalf("invalid or recursive command accepted: %q", command)
		}
	}
}
