package body

import (
	"mineagent/internal/config"
	"mineagent/internal/protocol"
	"testing"
)

func TestZonesOnlyRestrictEditedTargetBlocks(t *testing.T) {
	s, _, _, _ := setup(t)
	outside := [3]float64{100, 64, 100}
	for _, name := range []string{"body_goto", "body_look_at", "body_say", "body_command", "body_interact", "body_use_item"} {
		sp := protocol.BodySpec{Tool: name, WorldID: "yard", Dimension: "minecraft:overworld", Pos: outside, Target: &outside, Args: protocol.BodyArgs{Command: "help", Item: "apple"}}
		if err := s.policy(sp); err != nil {
			t.Fatalf("%s incorrectly depends on an edit zone: %v", name, err)
		}
	}
	for _, mode := range []string{"allowlist", "unrestricted"} {
		s.cfg.Zones.Mode = mode
		for _, name := range []string{"body_dig", "body_place"} {
			sp := protocol.BodySpec{Tool: name, WorldID: "yard", Dimension: "minecraft:overworld", Pos: outside, Target: &outside}
			if err := s.policy(sp); (err == nil) != (mode == "unrestricted") {
				t.Fatal(mode, name, err)
			}
		}
	}
	s.cfg.Zones = config.BodyZones{Mode: "allowlist", Areas: []config.BodyArea{{WorldID: "yard", Min: [3]float64{1, 60, 0}, Max: [3]float64{10, 80, 10}}}}
	target := [3]float64{1, 64, 0}
	sp := protocol.BodySpec{Tool: "body_place", WorldID: "yard", Dimension: "minecraft:overworld", Pos: [3]float64{0, 64, 0}, Target: &target}
	if err := s.policy(sp); err != nil {
		t.Fatal("standing outside an edit zone prevented reaching an allowed block", err)
	}
}

func TestSpawnProtectionConstrainsWritesButNotMovement(t *testing.T) {
	p := &BlockProtection{Enabled: true, Ready: true, World: "world", CenterX: 158, CenterZ: -41, Radius: 512}
	for _, tool := range []string{"body_dig", "body_place"} {
		for _, x := range []float64{-354, 158, 670} {
			target := [3]float64{x, -64, -41}
			sp := protocol.BodySpec{Tool: tool, WorldID: "world", Target: &target}
			if err := CheckBlockProtection(sp, p); err == nil || err.Error() != "spawn_protected" {
				t.Fatal(tool, x, err)
			}
		}
		for _, x := range []float64{-355, 671} {
			target := [3]float64{x, 75, -41}
			sp := protocol.BodySpec{Tool: tool, WorldID: "world", Target: &target}
			if err := CheckBlockProtection(sp, p); err != nil {
				t.Fatal("outside edit blocked", err)
			}
			if err := CheckBlockProtection(sp, nil); err == nil {
				t.Fatal("missing server guard state accepted")
			}
		}
	}
	for _, tool := range []string{"body_goto", "body_look_at", "body_command", "body_say", "body_observe"} {
		target := [3]float64{158, 75, -41}
		sp := protocol.BodySpec{Tool: tool, WorldID: "world", Target: &target}
		if err := CheckBlockProtection(sp, nil); err != nil {
			t.Fatal("movement depends on server edit guard", err)
		}
	}
}

func TestSeedPlacementCannotBypassCropDeny(t *testing.T) {
	s, _, _, _ := setup(t)
	target := [3]float64{1, 64, 1}
	sp := protocol.BodySpec{Tool: "body_place", WorldID: "yard", Dimension: "minecraft:overworld", Pos: [3]float64{0, 64, 0}, Target: &target, Block: "air", Args: protocol.BodyArgs{Item: "minecraft:wheat_seeds"}}
	if err := s.policy(sp); err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []string{"wheat", "minecraft:wheat", "wheat_seeds"} {
		s.cfg.DenyBlocks = []string{blocked}
		if err := s.policy(sp); err == nil || err.Error() != "denied_block" {
			t.Fatalf("seed bypassed %s: %v", blocked, err)
		}
	}
	s.cfg.DenyBlocks = nil
	s.cfg.DenyItems = []string{"wheat_seeds"}
	if err := s.policy(sp); err == nil || err.Error() != "denied_item" {
		t.Fatal(err)
	}
}
