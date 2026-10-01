package games

import (
	"encoding/json"
	"fmt"
	"strings"

	"mineagent/internal/games/chess"
)

// chessMatch 把 internal/games/chess 的规则适配成 Match。
// 悔棋用局面快照栈（每一步落子前 clone 一份，撤销即回退到上一份）。
type chessMatch struct {
	b     *chess.Board
	prev  []*chess.Board // 长度 = 已走步数；prev[i] 是第 i 步落子前的局面
	sans  []string
	ucis  []string
	plies []chessPly // 每一步的详情（谁、什么子、从哪到哪、吃了什么）
}

// chessPly 是一步的完整描述，给 AI 解说喂上下文用。
type chessPly struct {
	Side    string // 白方/黑方
	Piece   string // 兵/马/象/车/后/王
	From    string // e2
	To      string // e4
	Capture string // 被吃的子（空 = 没吃）
	San     string
	Check   bool
	Mate    bool
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
	moving := m.b.Squares[from]
	res, err := m.b.Play(from, to, promo)
	if err != nil {
		return PlayOutcome{}, err
	}
	m.prev = append(m.prev, before)
	m.sans = append(m.sans, res.SAN)
	m.ucis = append(m.ucis, res.Move.String())
	m.plies = append(m.plies, chessPly{
		Side:    sideLabelCN(side),
		Piece:   pieceCN(moving),
		From:    chess.Algebraic(from),
		To:      chess.Algebraic(to),
		Capture: pieceCN(res.CapturedPI),
		San:     res.SAN,
		Check:   res.Check,
		Mate:    res.Checkmate,
	})
	out := PlayOutcome{Move: res.SAN, Raw: res.Move.String(), Last: res.Move.String()}
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
func (m *chessMatch) Undo(side string) ([]string, []string, string, error) {
	if err := m.CanUndo(side); err != nil {
		return nil, nil, "", err
	}
	// 回退一步：回退后轮到走的一方就是刚被撤销的那一步的走子方。
	m.b = m.prev[len(m.prev)-1]
	m.prev = m.prev[:len(m.prev)-1]
	m.sans = m.sans[:len(m.sans)-1]
	m.ucis = m.ucis[:len(m.ucis)-1]
	if len(m.plies) > 0 {
		m.plies = m.plies[:len(m.plies)-1]
	}
	if m.Turn() != side && len(m.prev) > 0 {
		// 最后一步是对手走的、且自己之前也走过：再退一步
		m.b = m.prev[len(m.prev)-1]
		m.prev = m.prev[:len(m.prev)-1]
		m.sans = m.sans[:len(m.sans)-1]
		m.ucis = m.ucis[:len(m.ucis)-1]
		if len(m.plies) > 0 {
			m.plies = m.plies[:len(m.plies)-1]
		}
	}
	last := ""
	if n := len(m.ucis); n > 0 {
		last = m.ucis[n-1]
	}
	return append([]string(nil), m.sans...), append([]string(nil), m.ucis...), last, nil
}

// pieceCN 棋子英文代号 -> 中文（空返回空串）。
func pieceCN(p byte) string {
	switch p {
	case 'P', 'p':
		return "兵"
	case 'N', 'n':
		return "马"
	case 'B', 'b':
		return "象"
	case 'R', 'r':
		return "车"
	case 'Q', 'q':
		return "后"
	case 'K', 'k':
		return "王"
	}
	return ""
}

func sideLabelCN(side string) string {
	if side == "white" {
		return "白方"
	}
	return "黑方"
}

// materialCN 双方剩余子力（按子数+子名，例如 "白方：后1 车2 象2 马2 兵6，共 14 子"）。
func (m *chessMatch) materialCN() (string, string) {
	count := func(upper bool) map[byte]int {
		out := map[byte]int{}
		for _, p := range m.b.Squares {
			if p == 0 {
				continue
			}
			isUpper := p >= 'A' && p <= 'Z'
			if isUpper != upper {
				continue
			}
			if isUpper {
				out['A'+p-'A']++
			} else {
				out['A'+p-'a']++
			}
		}
		return out
	}
	// 'A'..'F' 从代号映射容易绕，这里直接用中文键
	fill := func(upper bool) string {
		names := []struct {
			code byte
			cn   string
		}{{'P', "兵"}, {'N', "马"}, {'B', "象"}, {'R', "车"}, {'Q', "后"}, {'K', "王"}}
		var parts []string
		total := 0
		for _, n := range names {
			c := count(upper)[n.code]
			if c > 0 {
				if n.cn == "兵" {
					parts = append(parts, "兵"+fmt.Sprint(c))
				} else {
					parts = append(parts, n.cn+fmt.Sprint(c))
				}
				total += c
			}
		}
		if total == 0 {
			return "（无子）"
		}
		return strings.Join(parts, " ") + "，共 " + fmt.Sprint(total) + " 子"
	}
	return fill(true), fill(false)
}

// asciiBoard 8 行棋盘（大写白、小写黑、. 空），方便模型直观看局面。
func (m *chessMatch) asciiBoard() string {
	var sb strings.Builder
	for r := 7; r >= 0; r-- {
		sb.WriteString(fmt.Sprint(r+1) + " ")
		for f := 0; f < 8; f++ {
			p := m.b.Squares[r*8+f]
			if p == 0 {
				sb.WriteByte('.')
			} else {
				sb.WriteByte(p)
			}
		}
		sb.WriteByte('\n')
	}
	sb.WriteString("  abcdefgh")
	return sb.String()
}

// PromptContext 给解说用：刚刚这一步的完整细节 + 对局进程 + 双方剩余子力 + 当前局面。
func (m *chessMatch) PromptContext() string {
	var sb strings.Builder
	sb.WriteString("国际象棋对局。\n")
	if len(m.plies) == 0 {
		return sb.String() + "还没有落子。"
	}
	last := m.plies[len(m.plies)-1]
	fmt.Fprintf(&sb, "刚刚：%s用「%s」从 %s 走到 %s", last.Side, last.Piece, last.From, last.To)
	if last.Capture != "" {
		fmt.Fprintf(&sb, "，吃掉了对方的「%s」", last.Capture)
	}
	switch {
	case last.Mate:
		sb.WriteString("，形成将杀！")
	case last.Check:
		sb.WriteString("，将军！")
	default:
		sb.WriteString("。")
	}
	sb.WriteString("\n")

	// 对局进程（最近 16 手，带 谁/什么子/从哪到哪/吃子）
	start := 0
	if len(m.plies) > 16 {
		start = len(m.plies) - 16
	}
	sb.WriteString("对局进程（最近 " + fmt.Sprint(len(m.plies)-start) + " 手）：\n")
	for i := start; i < len(m.plies); i++ {
		p := m.plies[i]
		no := i/2 + 1
		prefix := fmt.Sprintf("%d.%s ", no, p.Side)
		if i%2 == 1 {
			prefix = "   "
		}
		line := fmt.Sprintf("%s%s %s→%s（%s）", prefix, p.San, p.From, p.To, p.Piece)
		if p.Capture != "" {
			line += "×吃" + p.Capture
		}
		if p.Mate {
			line += " 将杀"
		} else if p.Check {
			line += " 将军"
		}
		sb.WriteString(line + "\n")
	}

	white, black := m.materialCN()
	sb.WriteString("双方剩余子力：白方 " + white + "；黑方 " + black + "\n")
	sb.WriteString("当前棋盘（大写白子、小写黑子、. 空）：\n" + m.asciiBoard() + "\n")
	sb.WriteString("当前局面 FEN：" + m.b.FEN() + "\n")
	sb.WriteString("轮到" + sideLabelCN(m.Turn()) + "走。")
	return sb.String()
}

func otherSide(side string) string {
	if side == "white" {
		return "black"
	}
	return "white"
}
