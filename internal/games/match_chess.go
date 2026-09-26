package games

import (
	"encoding/json"
	"strings"

	"mineagent/internal/games/chess"
)

// chessMatch 把 internal/games/chess 的规则适配成 Match。
type chessMatch struct {
	b *chess.Board
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
	res, err := m.b.Play(from, to, promo)
	if err != nil {
		return PlayOutcome{}, err
	}
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

func otherSide(side string) string {
	if side == "white" {
		return "black"
	}
	return "white"
}
