package body

import (
	"errors"
	"math"
	"mineagent/internal/protocol"
)

// BlockProtection is reported by the authenticated Paper channel from its live
// spawn guard. Never derive this geometry from model text or duplicate config.
type BlockProtection struct {
	Enabled bool   `json:"enabled"`
	Ready   bool   `json:"ready"`
	World   string `json:"world"`
	CenterX int    `json:"centerX"`
	CenterZ int    `json:"centerZ"`
	Radius  int    `json:"radius"`
}

func CheckBlockProtection(sp protocol.BodySpec, protection *BlockProtection) error {
	if sp.Tool != "body_dig" && sp.Tool != "body_place" {
		return nil
	}
	if protection == nil || !protection.Ready {
		return errors.New("protection_unavailable")
	}
	if !protection.Enabled {
		return nil
	}
	if protection.World == "" || protection.Radius < 1 || protection.Radius > 30000000 || sp.Target == nil || !finitePos(*sp.Target) {
		return errors.New("protection_unavailable")
	}
	if sp.WorldID == protection.World && math.Abs(sp.Target[0]-float64(protection.CenterX)) <= float64(protection.Radius) && math.Abs(sp.Target[2]-float64(protection.CenterZ)) <= float64(protection.Radius) {
		return errors.New("spawn_protected")
	}
	return nil
}
