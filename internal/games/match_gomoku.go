package games

import (
	"encoding/json"

	"mineagent/internal/games/gomoku"
)

// gomokuMatch 把 internal/games/gomoku 的规则适配成 Match。
type gomokuMatch struct {
	b     *gomoku.Board
	line  []int    // 获胜连线（终局高亮）
	pts   []string // 已落子的坐标（记谱 + 悔棋）
	order [][2]int // 与 pts 对应的 (row,col)
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
	m.pts = append(m.pts, name)
	m.order = append(m.order, [2]int{row, col})
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

// CanUndo 请求者是否至少落过一子（黑先，所以黑第 1 手 / 白第 2 手起可悔）。
func (m *gomokuMatch) CanUndo(side string) error {
	n := len(m.pts)
	if (side == "black" && n == 0) || (side == "white" && n < 2) {
		return errNoOwnMove
	}
	return nil
}

// Undo 撤销"请求者最近一手 + 其后的对手回应"（1-2 步），回到请求者重走。
func (m *gomokuMatch) Undo(side string) ([]string, string, error) {
	if err := m.CanUndo(side); err != nil {
		return nil, "", err
	}
	// 最后一步是谁落的：轮到谁走，谁就不是刚落子的一方
	lastSide := otherSide(gomoku.SideName(m.b.Turn))
	remove := 1
	if lastSide != side {
		remove = 2
	}
	for i := 0; i < remove && len(m.order) > 0; i++ {
		row, col := m.order[len(m.order)-1][0], m.order[len(m.order)-1][1]
		m.order = m.order[:len(m.order)-1]
		m.pts = m.pts[:len(m.pts)-1]
		m.b.Cells[row*gomoku.Size+col] = gomoku.Empty
		m.b.Moves--
		if m.b.Turn == gomoku.Black {
			m.b.Turn = gomoku.White
		} else {
			m.b.Turn = gomoku.Black
		}
	}
	m.line = nil
	last := ""
	if n := len(m.pts); n > 0 {
		last = m.pts[n-1]
	}
	return append([]string(nil), m.pts...), last, nil
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
