package games

import (
	"encoding/json"
	"strings"

	"mineagent/internal/games/chess"
)

// chessMatch 把 internal/games/chess 的规则适配成 Match。
// 悔棋用局面快照栈（每一步落子前 clone 一份，撤销即回退到上一份）。
type chessMatch struct {
	b    *chess.Board
	prev []*chess.Board // 长度 = 已走步数；prev[i] 是第 i 步落子前的局面
	sans []string
	ucis []string
}

func newChessMatch() Match { return &chessMatch{b: chess.Start()} }

func sideColor(side string) chess.Color {
	if side == "black" {
		return chess.Black
	}
	return chess.White
}

func (m *chessMatch) Turn() string {
	if m.b.Turn == chess.Black {
		return "black"
	}
	return "white"
}

func (m *chessMatch) Play(side string, payload json.RawMessage) (PlayOutcome, error) {
	var req struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Promotion string `json:"promotion"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return PlayOutcome{}, err
	}
	from, ok1 := chess.ParseSquare(strings.ToLower(strings.TrimSpace(req.From)))
	to, ok2 := chess.ParseSquare(strings.ToLower(strings.TrimSpace(req.To)))
	if !ok1 || !ok2 {
		return PlayOutcome{}, errBadMove
	}
	var promo byte
	if len(req.Promotion) == 1 {
		promo = strings.ToLower(req.Promotion)[0]
	}
	if m.b.Turn != sideColor(side) {
		return PlayOutcome{}, errNotYourTurn
	}
	before := m.b.Clone() // 落子前的局面（悔棋用）
	res, err := m.b.Play(from, to, promo)
	if err != nil {
		return PlayOutcome{}, err
	}
	m.prev = append(m.prev, before)
	m.sans = append(m.sans, res.SAN)
	m.ucis = append(m.ucis, res.Move.String())
	out := PlayOutcome{Move: res.SAN, Last: res.Move.String()}
	switch {
	case res.Checkmate:
		out.Over, out.Winner, out.Reason = true, side, "checkmate"
	case res.Stalemate:
		out.Over, out.Winner, out.Reason = true, "draw", "stalemate"
	}
	return out, nil
}

func (m *chessMatch) Snapshot(side string) map[string]any {
	snap := map[string]any{
		"pieces":  m.b.Pieces(),
		"inCheck": m.b.InCheck(m.b.Turn),
	}
	if side != "" && m.Turn() == side {
		legal := m.b.LegalMoves()
		list := make([]string, 0, len(legal))
		for _, mv := range legal {
			list = append(list, mv.String())
		}
		snap["legalMoves"] = list
	}
	return snap
}

// CanUndo 请求者是否至少走过一步（白先，所以白第 1 手 / 黑第 2 手起可悔）。
func (m *chessMatch) CanUndo(side string) error {
	n := len(m.prev)
	if (side == "white" && n == 0) || (side == "black" && n < 2) {
		return errNoOwnMove
	}
	return nil
}

// Undo 撤销"请求者最近一手 + 其后的对手回应"（1-2 步），回到请求者重走。
func (m *chessMatch) Undo(side string) ([]string, string, error) {
	if err := m.CanUndo(side); err != nil {
		return nil, "", err
	}
	// 回退一步：回退后轮到走的一方就是刚被撤销的那一步的走子方。
	m.b = m.prev[len(m.prev)-1]
	m.prev = m.prev[:len(m.prev)-1]
	m.sans = m.sans[:len(m.sans)-1]
	m.ucis = m.ucis[:len(m.ucis)-1]
	if m.Turn() != side && len(m.prev) > 0 {
		// 最后一步是对手走的、且自己之前也走过：再退一步
		m.b = m.prev[len(m.prev)-1]
		m.prev = m.prev[:len(m.prev)-1]
		m.sans = m.sans[:len(m.sans)-1]
		m.ucis = m.ucis[:len(m.ucis)-1]
	}
	last := ""
	if n := len(m.ucis); n > 0 {
		last = m.ucis[n-1]
	}
	return append([]string(nil), m.sans...), last, nil
}

func otherSide(side string) string {
	if side == "white" {
		return "black"
	}
	return "white"
}
