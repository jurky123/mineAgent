package games

import (
	"encoding/json"

	"mineagent/internal/games/gomoku"
)

// gomokuMatch 把 internal/games/gomoku 的规则适配成 Match。
type gomokuMatch struct {
	b    *gomoku.Board
	line []int // 获胜连线（终局高亮）
}

func newGomokuMatch() Match { return &gomokuMatch{b: gomoku.New()} }

func (m *gomokuMatch) Turn() string { return m.b.TurnSide() }

func (m *gomokuMatch) Play(side string, payload json.RawMessage) (PlayOutcome, error) {
	var req struct {
		Point string `json:"point"`
		Row   *int   `json:"row"`
		Col   *int   `json:"col"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return PlayOutcome{}, err
	}
	var (
		row, col int
		ok       bool
	)
	switch {
	case req.Point != "":
		row, col, ok = gomoku.ParsePoint(req.Point)
	case req.Row != nil && req.Col != nil:
		row, col, ok = *req.Row, *req.Col, true
	}
	if !ok {
		return PlayOutcome{}, errBadMove
	}
	if m.b.TurnSide() != side {
		return PlayOutcome{}, errNotYourTurn
	}
	win, line, err := m.b.Place(row, col)
	if err != nil {
		return PlayOutcome{}, err
	}
	name := gomoku.PointName(row, col)
	out := PlayOutcome{Move: name, Last: name}
	switch {
	case win:
		m.line = line
		out.Over, out.Winner, out.Reason = true, side, "five"
	case m.b.Full():
		out.Over, out.Winner, out.Reason = true, "draw", "full"
	}
	return out, nil
}

func (m *gomokuMatch) Snapshot(side string) map[string]any {
	snap := map[string]any{
		"size":  gomoku.Size,
		"cells": m.b.CellsString(),
	}
	if len(m.line) > 0 {
		snap["winLine"] = m.line
	}
	return snap
}
