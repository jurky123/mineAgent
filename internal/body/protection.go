package body

import (
	"errors"
	"math"
	"strings"

	"mineagent/internal/protocol"
)

var ErrSpawnProtected = errors.New("spawn_protected")

// WorldEdit commands which only inspect or change the selection/clipboard. All
// other // commands can write blocks and need the protected-area check.
var worldEditSafe = map[string]bool{
	"pos1": true, "pos2": true, "hpos1": true, "hpos2": true, "pos": true,
	"sel": true, "desel": true, "size": true, "count": true, "distr": true,
	"help": true, "wand": true, "toggleeditwand": true, "limit": true,
	"searchitem": true, "placement": true, "globalmask": true, "gmask": true,
	"expand": true, "contract": true, "shift": true, "outset": true,
	"inset": true, "trim": true, "chunk": true, "copy": true, "rotate": true,
	"flip": true, "clearclipboard": true,
}

// WorldEditWrite deliberately treats unknown // subcommands as writes. The
// Paper guard remains the final authority if this heuristic is wrong.
func WorldEditWrite(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "//") {
		return false
	}
	name := strings.ToLower(strings.TrimLeft(fields[0], "/"))
	if i := strings.LastIndex(name, ":"); i >= 0 {
		name = name[i+1:]
	}
	return !worldEditSafe[name]
}

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
		return ErrSpawnProtected
	}
	return nil
}
