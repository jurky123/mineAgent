package body

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mineagent/internal/protocol"
)

func TestGamePrincipalRequiresDirectControl(t *testing.T) {
	s, _, _, _ := setup(t)
	ctx := context.Background()
	p := s.GamePrincipal("Admin", "uuid-1")
	if err := s.Check(ctx, p, true); err == nil {
		t.Fatal("game principal accepted while directControl is off")
	}
	s.cfg.DirectControl = true
	if err := s.Check(ctx, p, true); err != nil {
		t.Fatalf("game principal rejected with directControl: %v", err)
	}
	if got := s.requester(p); got != "mc:uuid-1" {
		t.Fatalf("game requester = %q", got)
	}
}

func TestProtectedAreaIsTheOnlyApprovalGate(t *testing.T) {
	s, _, _, _ := setup(t)
	s.cfg.Approval.ProtectedAreaOnly = true
	target := [3]float64{1, 64, 1}
	sp := protocol.BodySpec{
		Tool: "body_place", WorldID: "yard", Dimension: "minecraft:overworld",
		Pos: [3]float64{0, 64, 0}, Target: &target, Block: "stone", Args: protocol.BodyArgs{Item: "minecraft:weapon"},
	}
	sp.Protected = true
	if !s.highRisk(sp) {
		t.Fatal("protected write was not approval-gated")
	}
	if err := s.reason(sp); err == nil || !strings.Contains(err.Error(), "reason_required") {
		t.Fatalf("protected write without reason accepted: %v", err)
	}
	sp.Args.Reason = "protected test"
	if err := s.reason(sp); err != nil {
		t.Fatalf("protected write with reason rejected: %v", err)
	}
	sp.Protected = false
	if s.highRisk(sp) {
		t.Fatal("outside protected area still requires approval")
	}
}

func TestCheckBlockProtectionSentinel(t *testing.T) {
	inside := [3]float64{100, 64, 100}
	outside := [3]float64{1000, 64, 1000}
	protection := &BlockProtection{Enabled: true, Ready: true, World: "world", CenterX: 100, CenterZ: 100, Radius: 32}
	if err := CheckBlockProtection(protocol.BodySpec{Tool: "body_place", WorldID: "world", Target: &inside}, protection); !errors.Is(err, ErrSpawnProtected) {
		t.Fatalf("inside error = %v", err)
	}
	if err := CheckBlockProtection(protocol.BodySpec{Tool: "body_place", WorldID: "world", Target: &outside}, protection); err != nil {
		t.Fatalf("outside error = %v", err)
	}
}

func TestProtectedOnlyAllowsUnsafeItemOutside(t *testing.T) {
	s, _, _, _ := setup(t)
	s.cfg.Approval.ProtectedAreaOnly = true
	sp := protocol.BodySpec{
		Tool: "body_use_item", WorldID: "yard", Dimension: "minecraft:overworld",
		Pos: [3]float64{0, 64, 0}, Args: protocol.BodyArgs{Item: "minecraft:lava_bucket"},
	}
	if err := s.policy(sp); err != nil {
		t.Fatalf("unsafe item outside protection rejected: %v", err)
	}
}

func TestWorldEditWriteClassifier(t *testing.T) {
	for _, command := range []string{"//set stone", "//paste", "//move 1 0 0", "//undo", "//walls stone", "//sphere stone 5"} {
		if !WorldEditWrite(command) {
			t.Fatalf("%q should be treated as a write", command)
		}
	}
	for _, command := range []string{"//pos1", "//pos2 1,64,1", "//copy", "//size", "//expand 1", "//rotate 90", "give stone", "/help"} {
		if WorldEditWrite(command) {
			t.Fatalf("%q should not be treated as a world write", command)
		}
	}
}

func TestProtectedCommandDoesNotRequireReason(t *testing.T) {
	s, _, _, _ := setup(t)
	s.cfg.Approval.ProtectedAreaOnly = true
	sp := protocol.BodySpec{
		Tool: "body_command", WorldID: "yard", Dimension: "minecraft:overworld",
		Pos: [3]float64{0, 64, 0}, Args: protocol.BodyArgs{Command: "//set stone"}, Protected: true,
	}
	if !s.highRisk(sp) {
		t.Fatal("protected command not approval-gated")
	}
	if err := s.reason(sp); err != nil {
		t.Fatalf("protected command rejected without reason: %v", err)
	}
}

func TestTeleportCommandClassifier(t *testing.T) {
	for _, command := range []string{"tp Oathtory", "/tp Oathtory", "tppos 328 64 529", "essentials:tp Oathtory"} {
		if !TeleportCommand(command) {
			t.Fatalf("%q should be a teleport", command)
		}
	}
	for _, command := range []string{"give stone", "//set stone", "tpaccept"} {
		if TeleportCommand(command) {
			t.Fatalf("%q should not be a teleport", command)
		}
	}
}
