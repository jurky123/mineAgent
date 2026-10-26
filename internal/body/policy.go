package body

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"mineagent/internal/protocol"
)

var hardBlocks = []string{"bedrock", "barrier", "command_block", "chain_command_block", "repeating_command_block", "structure_block", "structure_void", "jigsaw", "end_portal_frame", "spawner"}
var riskItems = []string{"tnt", "tnt_minecart", "lava_bucket", "water_bucket", "flint_and_steel", "fire_charge", "respawn_anchor", "end_crystal", "firework_rocket", "bow", "crossbow", "trident", "wind_charge"}
var safeFoods = []string{"apple", "golden_apple", "enchanted_golden_apple", "bread", "baked_potato", "carrot", "golden_carrot", "cooked_beef", "cooked_porkchop", "cooked_chicken", "cooked_mutton", "cooked_rabbit", "cooked_cod", "cooked_salmon", "melon_slice", "beetroot", "pumpkin_pie", "cookie", "honey_bottle"}
var itemID = regexp.MustCompile(`^(minecraft:)?[a-z0-9_]+$`)

func itemName(v string) string { return strings.TrimPrefix(v, "minecraft:") }
func placedBlock(item string) string {
	if itemName(item) == "wheat_seeds" {
		return "wheat"
	}
	return itemName(item)
}
func contains(list []string, v string) bool {
	v = itemName(v)
	for _, x := range list {
		if itemName(x) == v {
			return true
		}
	}
	return false
}
func isRead(t string) bool {
	return t == "body_status" || t == "body_observe" || t == "body_inventory" || t == "body_find_blocks"
}
func category(t string) string {
	if isRead(t) {
		return "read"
	}
	switch t {
	case "body_goto", "body_look_at":
		return "move"
	case "body_say":
		return "chat"
	case "body_command":
		return "command"
	case "body_stop":
		return "control"
	default:
		return "build"
	}
}
func capability(t string) string {
	switch t {
	case "body_inventory", "body_find_blocks", "body_observe":
		return "observe"
	case "body_goto", "body_look_at", "body_stop":
		return "move"
	case "body_dig":
		return "dig"
	case "body_place":
		return "place"
	case "body_interact":
		return "interact"
	case "body_use_item":
		return "item"
	case "body_say":
		return "chat"
	case "body_command":
		return "command"
	}
	return "unsupported"
}
func invalid(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
func parseArgs(tool, raw string) (protocol.BodyArgs, error) {
	var a protocol.BodyArgs
	if len(raw) > 4096 {
		return a, errors.New("invalid_args")
	}
	if raw == "" {
		raw = "{}"
	}
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.DisallowUnknownFields()
	if d.Decode(&a) != nil {
		return a, errors.New("invalid_args")
	}
	if d.Decode(new(any)) != io.EOF {
		return a, errors.New("invalid_args")
	}
	if a.TimeoutSec < 0 || a.TimeoutSec > 20 || a.Range < 0 || a.Range > 16 || a.Radius < 0 || a.Radius > 32 || a.Count < 0 || a.Count > 16 || a.HoldMS < 0 || a.HoldMS > 2000 || len(a.Filter) > 100 || len(a.Blocks) > 500 || utf8.RuneCountInString(a.Reason) > 60 || invalid(a.Reason) {
		return a, errors.New("invalid_args")
	}
	for _, v := range []*float64{a.X, a.Y, a.Z} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || math.Abs(*v) > 30000000) {
			return a, errors.New("invalid_args")
		}
	}
	if a.Movement != "" && (tool != "body_goto" || !contains([]string{"ground", "swim"}, a.Movement)) {
		return a, errors.New("invalid_args")
	}
	xyz := a.X != nil && a.Y != nil && a.Z != nil
	anyXYZ := a.X != nil || a.Y != nil || a.Z != nil
	xz := a.X != nil && a.Z != nil
	if anyXYZ && !xyz && !(tool == "body_goto" && xz) {
		return a, errors.New("invalid_args")
	}
	if a.Item != "" && !itemID.MatchString(a.Item) {
		return a, errors.New("invalid_args")
	}
	switch tool {
	case "body_goto", "body_look_at":
		n := 0
		if xyz || tool == "body_goto" && xz {
			n++
		}
		if a.Player != "" {
			n++
		}
		if a.EntityID != nil {
			n++
		}
		if n != 1 || (tool == "body_goto" && a.EntityID != nil) || a.Range > 8 {
			return a, errors.New("invalid_args")
		}
	case "body_dig", "body_place", "body_interact":
		if !xyz || a.EntityID != nil {
			return a, errors.New("unsupported_target")
		}
		for _, v := range []*float64{a.X, a.Y, a.Z} {
			if *v != math.Trunc(*v) {
				return a, errors.New("invalid_args")
			}
		}
	case "body_find_blocks":
		if strings.TrimSpace(a.Blocks) == "" {
			return a, errors.New("invalid_args")
		}
		for _, b := range strings.Split(a.Blocks, ",") {
			if !itemID.MatchString(strings.TrimSpace(b)) {
				return a, errors.New("invalid_args")
			}
		}
	case "body_say":
		if strings.TrimSpace(a.Text) == "" || strings.HasPrefix(strings.TrimSpace(a.Text), "/") || invalid(a.Text) || utf8.RuneCountInString(a.Text) > 256 || strings.Contains(strings.ToLower(a.Text), "@agent") || strings.Contains(strings.ToLower(a.Text), "@ai") {
			return a, errors.New("invalid_args")
		}
	case "body_command":
		a.Command = strings.TrimSpace(a.Command)
		if strings.TrimSpace(strings.TrimLeft(a.Command, "/")) == "" || invalid(a.Command) || utf8.RuneCountInString(a.Command) > 256 {
			return a, errors.New("invalid_args")
		}
		first := strings.ToLower(strings.Fields(a.Command)[0])
		if i := strings.LastIndex(first, ":"); i >= 0 {
			first = first[i+1:]
		}
		first = strings.TrimLeft(first, "/")
		if contains([]string{"agent", "ai", "ma", "mineagent"}, first) {
			return a, errors.New("loop_guard")
		}
	case "body_use_item", "body_status", "body_observe", "body_inventory", "body_stop":
	default:
		return a, errors.New("unsupported")
	}
	if a.Face != "" && !contains([]string{"up", "down", "north", "south", "east", "west"}, a.Face) {
		return a, errors.New("invalid_args")
	}
	if a.Player != "" && !regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`).MatchString(a.Player) {
		return a, errors.New("invalid_args")
	}
	return a, nil
}
func timeoutMS(t string, a protocol.BodyArgs) int {
	n := 5000
	switch t {
	case "body_goto":
		n = 12000
	case "body_dig":
		n = 15000
	case "body_observe", "body_say", "body_look_at":
		n = 3000
	case "body_inventory":
		n = 2000
	case "body_command":
		n = 10000
	}
	if a.TimeoutSec > 0 {
		v := a.TimeoutSec * 1000
		if t == "body_goto" && v <= 20000 {
			n = v
		}
		if t == "body_dig" && v <= 15000 {
			n = v
		}
	}
	return n
}
func (s *Service) inZone(world string, pos [3]float64, margin float64) bool {
	if !finitePos(pos) || world == "" {
		return false
	}
	if s.cfg.Zones.Mode == "unrestricted" {
		return true
	}
	if s.cfg.Zones.Mode != "allowlist" {
		return false
	}
	for _, a := range s.cfg.Zones.Areas {
		if a.WorldID != world {
			continue
		}
		ok := true
		for i, v := range pos {
			if v < a.Min[i]+margin || v > a.Max[i]-margin {
				ok = false
			}
		}
		if ok {
			return true
		}
	}
	return false
}
func (s *Service) policy(sp protocol.BodySpec) error {
	mapped := false
	for _, w := range s.cfg.WorldMappings {
		if w.WorldID == sp.WorldID && w.Dimension == sp.Dimension {
			mapped = true
		}
	}
	if !mapped {
		return errors.New("world_unknown")
	}
	if sp.Target != nil && !finitePos(*sp.Target) {
		return errors.New("invalid_args")
	}
	// Zones constrain changed blocks, never the player's position or movement.
	if (sp.Tool == "body_dig" || sp.Tool == "body_place") && (sp.Target == nil || !s.inZone(sp.WorldID, *sp.Target, 0)) {
		return errors.New("out_of_zone")
	}
	if contains(hardBlocks, sp.Block) || contains(s.cfg.DenyBlocks, sp.Block) || contains(hardBlocks, sp.Args.Item) || contains(s.cfg.DenyBlocks, sp.Args.Item) || (sp.Tool == "body_place" && contains(s.cfg.DenyBlocks, placedBlock(sp.Args.Item))) {
		return errors.New("denied_block")
	}
	for _, item := range []string{sp.Args.Item, sp.MainHand, sp.OffHand} {
		if item != "" && contains(s.cfg.DenyItems, item) {
			return errors.New("denied_item")
		}
	}
	if sp.Tool == "body_command" {
		first := strings.Fields(sp.Args.Command)
		if len(first) > 0 {
			cmd := strings.ToLower(first[0])
			if i := strings.LastIndex(cmd, ":"); i >= 0 {
				cmd = cmd[i+1:]
			}
			cmd = strings.TrimLeft(cmd, "/")
			if contains(s.cfg.LoopGuardCommands, cmd) {
				return errors.New("loop_guard")
			}
		}
	}
	if sp.Tool == "body_use_item" && !contains(safeFoods, sp.Args.Item) && !s.cfg.Approval.ProtectedAreaOnly && !s.highRisk(sp) {
		return errors.New("unsupported_item")
	}
	if sp.Target != nil && category(sp.Tool) == "build" {
		d := 0.0
		for i := 0; i < 3; i++ {
			d += math.Pow(sp.Pos[i]-sp.Target[i], 2)
		}
		if d > 25 {
			return errors.New("not_in_reach")
		}
	}
	return nil
}
func (s *Service) highRisk(sp protocol.BodySpec) bool {
	if s.cfg.Approval.ProtectedAreaOnly {
		return sp.Protected
	}
	if isRead(sp.Tool) || category(sp.Tool) == "move" || category(sp.Tool) == "chat" || category(sp.Tool) == "command" {
		return false
	}
	if len(sp.Hazards) > 0 {
		return true
	}
	for _, i := range []string{sp.Args.Item, sp.Block, sp.MainHand, sp.OffHand} {
		if i != "" && (contains(riskItems, i) || contains(s.cfg.HighRiskItems, i)) {
			return true
		}
		if strings.HasSuffix(i, "_bed") && sp.Dimension != "minecraft:overworld" {
			return true
		}
	}
	return false
}
func (s *Service) reason(sp protocol.BodySpec) error {
	// Commands have no reason parameter in their tool schema; the approval card
	// shows the actual command instead, so do not reject them for a blank reason.
	if s.highRisk(sp) && sp.Tool != "body_command" && strings.TrimSpace(sp.Args.Reason) == "" {
		return fmt.Errorf("reason_required")
	}
	return nil
}
