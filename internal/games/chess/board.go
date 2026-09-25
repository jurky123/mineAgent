// Package chess 是服务端权威的国际象棋规则引擎（最简可用版）。
//
// v1 支持：所有棋子走法、吃子、将军/将杀/逼和、王车易位、升变（默认后）。
// v1 暂不支持：吃过路兵、50 回合/三次重复和棋、认输判负之外的其它终局判定。
package chess

import (
	"fmt"
	"strings"
)

// Color 0=白 1=黑。
type Color int

const (
	White Color = 0
	Black Color = 1
)

func (c Color) String() string {
	if c == White {
		return "white"
	}
	return "black"
}

// Move 用坐标记法：From/To 是 0..63（file 0..7 = a..h，rank 0 = 第 1 横排）。
// Promotion 为 0 或 'q'/'r'/'b'/'n'（小写）。
type Move struct {
	From      int
	To        int
	Promotion byte
	// Capture/Check 仅用于展示（按走子前局面计算）。
	Capture bool
	Check   bool
}

// Board 是当前局面。
type Board struct {
	// Squares 索引 = rank*8+file；白方大写 PNBRQK，黑方小写，空位为 0。
	Squares [64]byte
	Turn    Color
	// Castling [颜色][0=短(王翼) 1=长(后翼)]。
	Castling [2][2]bool
	// Ply 已走的半回合数（用于展示/将来 50 回合规则）。
	Ply int
}

// Start 初始局面。
func Start() *Board {
	b := &Board{Turn: White}
	back := []byte{'R', 'N', 'B', 'Q', 'K', 'B', 'N', 'R'}
	for f := 0; f < 8; f++ {
		b.Squares[f] = back[f]
		b.Squares[8+f] = 'P'
		b.Squares[48+f] = 'p'
		b.Squares[56+f] = back[f] + 32
	}
	b.Castling = [2][2]bool{{true, true}, {true, true}}
	return b
}

// Clone 复制局面（校验非法走法时用）。
func (b *Board) Clone() *Board {
	c := *b
	return &c
}

func colorOf(p byte) Color {
	if p >= 'a' && p <= 'z' {
		return Black
	}
	return White
}

func isWhitePiece(p byte) bool { return p >= 'A' && p <= 'Z' }

// pieceColor 返回棋子颜色；空位/非法返回 -1。
func pieceColor(p byte) int {
	switch {
	case p >= 'A' && p <= 'Z':
		return 0
	case p >= 'a' && p <= 'z':
		return 1
	default:
		return -1
	}
}

// Algebraic 把格子编号转成 e2/e4 这种记法。
func Algebraic(sq int) string {
	if sq < 0 || sq > 63 {
		return "??"
	}
	return string(rune('a'+sq%8)) + string(rune('1'+sq/8))
}

// ParseSquare 解析 e2 -> 12。
func ParseSquare(s string) (int, bool) {
	if len(s) != 2 {
		return 0, false
	}
	f, r := s[0], s[1]
	if f < 'a' || f > 'h' || r < '1' || r > '8' {
		return 0, false
	}
	return int(r-'1')*8 + int(f-'a'), true
}

// MoveString 输出 e2e4 / e7e8q。
func (m Move) String() string {
	s := Algebraic(m.From) + Algebraic(m.To)
	if m.Promotion != 0 {
		s += string(m.Promotion)
	}
	return s
}

// ParseMove 解析 e2e4 / e7e8q。
func ParseMove(s string) (Move, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 4 && len(s) != 5 {
		return Move{}, false
	}
	from, ok1 := ParseSquare(s[0:2])
	to, ok2 := ParseSquare(s[2:4])
	if !ok1 || !ok2 {
		return Move{}, false
	}
	m := Move{From: from, To: to}
	if len(s) == 5 {
		switch s[4] {
		case 'q', 'r', 'b', 'n':
			m.Promotion = s[4]
		default:
			return Move{}, false
		}
	}
	return m, true
}

// ---------- 攻击判定 ----------

// Attacked 判断 sq 是否被 by 方攻击。
func (b *Board) Attacked(sq int, by Color) bool {
	f, r := sq%8, sq/8
	// 兵：白兵向上吃（rank+1），黑兵向下吃（rank-1）。
	if by == White {
		for _, df := range []int{-1, 1} {
			nf, nr := f+df, r-1
			if nf >= 0 && nf < 8 && nr >= 0 && nr < 8 && b.Squares[nr*8+nf] == 'P' {
				return true
			}
		}
	} else {
		for _, df := range []int{-1, 1} {
			nf, nr := f+df, r+1
			if nf >= 0 && nf < 8 && nr >= 0 && nr < 8 && b.Squares[nr*8+nf] == 'p' {
				return true
			}
		}
	}
	// 马
	for _, d := range [][2]int{{1, 2}, {2, 1}, {-1, 2}, {-2, 1}, {1, -2}, {2, -1}, {-1, -2}, {-2, -1}} {
		nf, nr := f+d[0], r+d[1]
		if nf < 0 || nf > 7 || nr < 0 || nr > 7 {
			continue
		}
		p := b.Squares[nr*8+nf]
		if pieceColor(p) == int(by) && (p == 'N' || p == 'n') {
			return true
		}
	}
	// 王
	for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
		nf, nr := f+d[0], r+d[1]
		if nf < 0 || nf > 7 || nr < 0 || nr > 7 {
			continue
		}
		p := b.Squares[nr*8+nf]
		if pieceColor(p) == int(by) && (p == 'K' || p == 'k') {
			return true
		}
	}
	// 车/后（直线）
	for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nf, nr := f+d[0], r+d[1]
		for nf >= 0 && nf < 8 && nr >= 0 && nr < 8 {
			p := b.Squares[nr*8+nf]
			if p != 0 {
				if pieceColor(p) == int(by) && (p == 'R' || p == 'r' || p == 'Q' || p == 'q') {
					return true
				}
				break
			}
			nf, nr = nf+d[0], nr+d[1]
		}
	}
	// 象/后（斜线）
	for _, d := range [][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
		nf, nr := f+d[0], r+d[1]
		for nf >= 0 && nf < 8 && nr >= 0 && nr < 8 {
			p := b.Squares[nr*8+nf]
			if p != 0 {
				if pieceColor(p) == int(by) && (p == 'B' || p == 'b' || p == 'Q' || p == 'q') {
					return true
				}
				break
			}
			nf, nr = nf+d[0], nr+d[1]
		}
	}
	return false
}

// FindKing 返回某方王的格子编号；找不到返回 -1。
func (b *Board) FindKing(c Color) int {
	want := byte('K')
	if c == Black {
		want = 'k'
	}
	for i, p := range b.Squares {
		if p == want {
			return i
		}
	}
	return -1
}

// InCheck 某方是否被将军。
func (b *Board) InCheck(c Color) bool {
	k := b.FindKing(c)
	if k < 0 {
		return false
	}
	other := White
	if c == White {
		other = Black
	}
	return b.Attacked(k, other)
}

// ---------- 走法生成 ----------

func onBoard(f, r int) bool { return f >= 0 && f < 8 && r >= 0 && r < 8 }

// pseudoMoves 生成伪合法走法（不检查是否把自家王置于被将状态）。
func (b *Board) pseudoMoves(c Color) []Move {
	var out []Move
	own := int(c)
	// 自己的棋子是白还是黑：pieceColor 与 c 一致才可动。
	mine := func(p byte) bool { return pieceColor(p) == own }
	enemyOrEmpty := func(p byte) bool { return pieceColor(p) != own }

	push := func(from, to int, promo byte) {
		m := Move{From: from, To: to, Promotion: promo}
		if p := b.Squares[to]; p != 0 {
			m.Capture = true
		}
		out = append(out, m)
	}

	for sq, p := range b.Squares {
		if p == 0 || !mine(p) {
			continue
		}
		f, r := sq%8, sq/8
		switch p | 0x20 { // 转小写统一判断
		case 'p':
			dir := 1
			startRank := 1
			promoRank := 7
			if c == Black {
				dir = -1
				startRank = 6
				promoRank = 0
			}
			// 前进一格
			if nr := r + dir; onBoard(f, nr) && b.Squares[nr*8+f] == 0 {
				if nr == promoRank {
					for _, q := range []byte{'q', 'r', 'b', 'n'} {
						push(sq, nr*8+f, q)
					}
				} else {
					push(sq, nr*8+f, 0)
					// 起步两格
					if r == startRank {
						if nr2 := r + 2*dir; b.Squares[nr2*8+f] == 0 {
							push(sq, nr2*8+f, 0)
						}
					}
				}
			}
			// 斜吃
			for _, df := range []int{-1, 1} {
				nf, nr := f+df, r+dir
				if !onBoard(nf, nr) {
					continue
				}
				t := nr*8 + nf
				if b.Squares[t] != 0 && enemyOrEmpty(b.Squares[t]) {
					if nr == promoRank {
						for _, q := range []byte{'q', 'r', 'b', 'n'} {
							push(sq, t, q)
						}
					} else {
						push(sq, t, 0)
					}
				}
			}
		case 'n':
			for _, d := range [][2]int{{1, 2}, {2, 1}, {-1, 2}, {-2, 1}, {1, -2}, {2, -1}, {-1, -2}, {-2, -1}} {
				nf, nr := f+d[0], r+d[1]
				if onBoard(nf, nr) && enemyOrEmpty(b.Squares[nr*8+nf]) {
					push(sq, nr*8+nf, 0)
				}
			}
		case 'b', 'r', 'q':
			var dirs [][2]int
			switch p | 0x20 {
			case 'b':
				dirs = [][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
			case 'r':
				dirs = [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
			default:
				dirs = [][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}, {1, 0}, {-1, 0}, {0, 1}, {0, -1}}
			}
			for _, d := range dirs {
				nf, nr := f+d[0], r+d[1]
				for onBoard(nf, nr) {
					t := nr*8 + nf
					if b.Squares[t] == 0 {
						push(sq, t, 0)
					} else {
						if enemyOrEmpty(b.Squares[t]) {
							push(sq, t, 0)
						}
						break
					}
					nf, nr = nf+d[0], nr+d[1]
				}
			}
		case 'k':
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
				nf, nr := f+d[0], r+d[1]
				if onBoard(nf, nr) && enemyOrEmpty(b.Squares[nr*8+nf]) {
					push(sq, nr*8+nf, 0)
				}
			}
			out = append(out, b.castlingMoves(c)...)
		}
	}
	return out
}

// castlingMoves 王车易位（要求：权利在、中间无子、王不在被将状态、经过的格子不被攻击）。
func (b *Board) castlingMoves(c Color) []Move {
	if b.InCheck(c) {
		return nil
	}
	rank := 0
	if c == Black {
		rank = 7
	}
	other := White
	if c == White {
		other = Black
	}
	king := rank*8 + 4
	var out []Move
	// 短易位：f/g 空，e/f/g 不被攻击
	if b.Castling[c][0] &&
		b.Squares[rank*8+5] == 0 && b.Squares[rank*8+6] == 0 &&
		!b.Attacked(rank*8+5, other) && !b.Attacked(rank*8+6, other) {
		out = append(out, Move{From: king, To: rank*8 + 6})
	}
	// 长易位：b/c/d 空，e/d/c 不被攻击
	if b.Castling[c][1] &&
		b.Squares[rank*8+1] == 0 && b.Squares[rank*8+2] == 0 && b.Squares[rank*8+3] == 0 &&
		!b.Attacked(rank*8+3, other) && !b.Attacked(rank*8+2, other) {
		out = append(out, Move{From: king, To: rank*8 + 2})
	}
	return out
}

// apply 执行一步（不校验合法性，内部用）。
func (b *Board) apply(m Move) {
	p := b.Squares[m.From]
	b.Squares[m.From] = 0
	if m.Promotion != 0 {
		if p == 'P' {
			p = byte(m.Promotion) - 32
		} else {
			p = m.Promotion
		}
	}
	b.Squares[m.To] = p
	// 易位时同步挪车
	if (p == 'K' || p == 'k') && abs(m.To-m.From) == 2 {
		rank := m.From / 8
		if m.To%8 == 6 { // 短易位
			b.Squares[rank*8+5] = b.Squares[rank*8+7]
			b.Squares[rank*8+7] = 0
		} else { // 长易位
			b.Squares[rank*8+3] = b.Squares[rank*8+0]
			b.Squares[rank*8+0] = 0
		}
	}
	// 更新易位权利：王动、车动、车被吃
	switch p | 0x20 {
	case 'k':
		b.Castling[colorOf(p)][0] = false
		b.Castling[colorOf(p)][1] = false
	case 'r':
		rank := m.From / 8
		if m.From%8 == 0 {
			b.Castling[colorOf(p)][1] = false
		} else if m.From%8 == 7 {
			b.Castling[colorOf(p)][0] = false
		}
		_ = rank
	}
	if p == 'R' || p == 'r' {
		// 车的初始格被吃/离开
		for _, c := range []Color{White, Black} {
			base := 0
			if c == Black {
				base = 56
			}
			if m.To == base+0 || m.From == base+0 {
				b.Castling[c][1] = false
			}
			if m.To == base+7 || m.From == base+7 {
				b.Castling[c][0] = false
			}
		}
	}
	b.Ply++
	if b.Turn == White {
		b.Turn = Black
	} else {
		b.Turn = White
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// LegalMoves 生成合法走法（会过滤掉把自家王置于被将状态的走法）。
func (b *Board) LegalMoves() []Move {
	var out []Move
	for _, m := range b.pseudoMoves(b.Turn) {
		nb := b.Clone()
		nb.apply(m)
		if !nb.InCheck(b.Turn) {
			out = append(out, m)
		}
	}
	return out
}

// MoveResult 是执行走法后的结果说明。
type MoveResult struct {
	Move       Move
	Check      bool
	Checkmate  bool
	Stalemate  bool
	Promotion  byte
	CapturedPI byte
}

// Play 校验并执行一步；非法返回 error。
func (b *Board) Play(from, to int, promotion byte) (MoveResult, error) {
	for _, m := range b.LegalMoves() {
		if m.From != from || m.To != to {
			continue
		}
		if m.Promotion != 0 && promotion != 0 && m.Promotion != promotion {
			continue
		}
		captured := b.Squares[m.To]
		res := MoveResult{Move: m, Promotion: m.Promotion, CapturedPI: captured}
		b.apply(m)
		opp := b.Turn
		res.Check = b.InCheck(opp)
		if res.Check {
			hasMove := false
			for _, om := range b.LegalMoves() {
				_ = om
				hasMove = true
				break
			}
			if !hasMove {
				res.Checkmate = true
			}
		} else {
			hasMove := false
			for _, om := range b.LegalMoves() {
				_ = om
				hasMove = true
				break
			}
			if !hasMove {
				res.Stalemate = true
			}
		}
		return res, nil
	}
	return MoveResult{}, fmt.Errorf("非法走法")
}

// ---------- FEN / 展示 ----------

// FEN 输出当前局面（用于测试与调试）。
func (b *Board) FEN() string {
	var sb strings.Builder
	for r := 7; r >= 0; r-- {
		empty := 0
		for f := 0; f < 8; f++ {
			p := b.Squares[r*8+f]
			if p == 0 {
				empty++
				continue
			}
			if empty > 0 {
				sb.WriteByte(byte('0' + empty))
				empty = 0
			}
			sb.WriteByte(p)
		}
		if empty > 0 {
			sb.WriteByte(byte('0' + empty))
		}
		if r > 0 {
			sb.WriteByte('/')
		}
	}
	turn := "w"
	if b.Turn == Black {
		turn = "b"
	}
	cast := ""
	if b.Castling[White][0] {
		cast += "K"
	}
	if b.Castling[White][1] {
		cast += "Q"
	}
	if b.Castling[Black][0] {
		cast += "k"
	}
	if b.Castling[Black][1] {
		cast += "q"
	}
	if cast == "" {
		cast = "-"
	}
	return fmt.Sprintf("%s %s %s - 0 %d", sb.String(), turn, cast, b.Ply/2+1)
}

// ParseFEN 解析 FEN（只取棋子布局/行棋方/易位权，用于测试）。
func ParseFEN(fen string) (*Board, error) {
	parts := strings.Fields(fen)
	if len(parts) < 2 {
		return nil, fmt.Errorf("FEN 不完整")
	}
	b := &Board{}
	rank := 7
	file := 0
	for _, ch := range parts[0] {
		switch {
		case ch == '/':
			rank--
			file = 0
		case ch >= '1' && ch <= '8':
			file += int(ch - '0')
		case strings.ContainsRune("PNBRQKpnbrqk", ch):
			if rank < 0 || file > 7 {
				return nil, fmt.Errorf("FEN 布局非法")
			}
			b.Squares[rank*8+file] = byte(ch)
			file++
		default:
			return nil, fmt.Errorf("FEN 非法字符 %q", ch)
		}
	}
	if parts[1] == "b" {
		b.Turn = Black
	}
	if len(parts) > 2 {
		for _, ch := range parts[2] {
			switch ch {
			case 'K':
				b.Castling[White][0] = true
			case 'Q':
				b.Castling[White][1] = true
			case 'k':
				b.Castling[Black][0] = true
			case 'q':
				b.Castling[Black][1] = true
			}
		}
	}
	return b, nil
}

// Pieces 输出 64 字符的棋盘（前端直接渲染）。
func (b *Board) Pieces() string {
	out := make([]byte, 64)
	for i, p := range b.Squares {
		if p == 0 {
			out[i] = '.'
		} else {
			out[i] = p
		}
	}
	return string(out)
}
