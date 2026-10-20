package body

import (
	"encoding/json"
	"math"

	"mineagent/internal/protocol"
)

type navigationResult struct {
	Partial          bool        `json:"partial"`
	Reached          bool        `json:"reached"`
	Position         *[3]float64 `json:"position"`
	Distance         float64     `json:"distance"`
	InitialDistance  float64     `json:"initialDistance"`
	ProgressDistance float64     `json:"progressDistance"`
	MovedDistance    float64     `json:"movedDistance"`
	Path             *struct {
		Status         string  `json:"status"`
		VisitedNodes   int     `json:"visitedNodes"`
		GeneratedNodes int     `json:"generatedNodes"`
		ElapsedMS      float64 `json:"elapsedMs"`
	} `json:"path"`
}

func navigation(r protocol.BodyResult) *navigationResult {
	var n navigationResult
	if json.Unmarshal(r.Data, &n) != nil || n.Position == nil || !finitePos(*n.Position) {
		return nil
	}
	if n.Path != nil && !contains([]string{"success", "partial", "timeout", "noPath"}, n.Path.Status) {
		n.Path = nil
	}
	return &n
}

// A partial walk can advance the task without reaching its destination. Use
// backend-held start/target and returned physical position, not a claimed flag
// or mere sideways movement, to reset the consecutive no-progress streak.
func navigationProgress(r protocol.BodyResult, sp *protocol.BodySpec) bool {
	if sp == nil || sp.Tool != "body_goto" || sp.Target == nil || r.Outcome != "confirmed" || !contains([]string{"failed", "path_not_found", "path_search_timeout", "timeout"}, r.Code) {
		return false
	}
	n := navigation(r)
	if n == nil || !n.Partial || n.Reached {
		return false
	}
	dist := func(a, b [3]float64) float64 {
		return math.Sqrt(math.Pow(a[0]-b[0], 2) + math.Pow(a[1]-b[1], 2) + math.Pow(a[2]-b[2], 2))
	}
	return finitePos(sp.Pos) && finitePos(*sp.Target) && dist(sp.Pos, *sp.Target)-dist(*n.Position, *sp.Target) >= 0.5
}

func (p *Principal) FailureSummary() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastTool == "" {
		return ""
	}
	return "最近失败动作：" + p.lastTool + "（" + p.lastCode + "）。"
}
