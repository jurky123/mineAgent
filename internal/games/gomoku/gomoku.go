// Package gomoku 是五子棋规则（15 路、黑先、横竖斜连成 5 子或以上即胜）。
// 简版：不做禁手/三三/长连判负（自由规则）。
package gomoku

import (
	"errors"
	"fmt"
)

// Size 棋盘路数（15x15 交叉点）。
const Size = 15

// 格子状态
const (
	Empty byte = iota
	Black
	White
)

var (
	ErrOccupied   = errors.New("这里已经有子了")
	ErrOutOfRange = errors.New("坐标超出棋盘")
)

// Board 是一局五子棋。
type Board struct {
	Cells [Size * Size]byte // 0 空 / 1 黑 / 2 白
	Turn  byte              // 该谁走
	Moves int
}

func New() *Board {
	return &Board{Turn: Black}
}

// SideName 返回该方的展示名（"black"/"white"）。
func SideName(c byte) string {
	if c == White {
		return "white"
	}
	return "black"
}

func (b *Board) TurnSide() string { return SideName(b.Turn) }

// Point 解析 "h8" 这样的坐标（列 a-o，行 1-15），返回 row, col（0 起）。
// 支持 a1..o15（列 1 个字母 + 行 1-2 位数字）。
func ParsePoint(s string) (int, int, bool) {
	s = trimLower(s)
	if len(s) < 2 {
		return 0, 0, false
	}
	col := int(s[0] - 'a')
	if col < 0 || col >= Size {
		return 0, 0, false
	}
	row := 0
	for _, ch := range s[1:] {
		if ch < '0' || ch > '9' {
			return 0, 0, false
		}
		row = row*10 + int(ch-'0')
	}
	row--
	if row < 0 || row >= Size {
		return 0, 0, false
	}
	return row, col, true
}

// PointName 输出 "h8" 形式的坐标。
func PointName(row, col int) string {
	return fmt.Sprintf("%c%d", 'a'+col, row+1)
}

func trimLower(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c == ' ' || c == '\t' {
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

// Place 在 (row,col) 落子；返回是否获胜及获胜的连线（用于高亮）。
func (b *Board) Place(row, col int) (win bool, line []int, err error) {
	if row < 0 || row >= Size || col < 0 || col >= Size {
		return false, nil, ErrOutOfRange
	}
	t := row*Size + col
	if b.Cells[t] != Empty {
		return false, nil, ErrOccupied
	}
	b.Cells[t] = b.Turn
	b.Moves++
	win, line = b.winAt(row, col)
	if !win {
		if b.Turn == Black {
			b.Turn = White
		} else {
			b.Turn = Black
		}
	}
	return win, line, nil
}

// Full 棋盘是否已满（和棋）。
func (b *Board) Full() bool { return b.Moves >= Size*Size }

// winAt 从刚落子的位置往 4 个方向数同色子，>=5 即胜。
func (b *Board) winAt(row, col int) (bool, []int) {
	color := b.Cells[row*Size+col]
	dirs := [][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for _, d := range dirs {
		line := []int{row*Size + col}
		for sign := -1; sign <= 1; sign += 2 {
			r, c := row+d[0]*sign, col+d[1]*sign
			for r >= 0 && r < Size && c >= 0 && c < Size && b.Cells[r*Size+c] == color {
				line = append(line, r*Size+c)
				r += d[0] * sign
				c += d[1] * sign
			}
		}
		if len(line) >= 5 {
			return true, line
		}
	}
	return false, nil
}

// CellsString 输出 Size*Size 个字符（. 空 / b 黑 / w 白），前端直接渲染。
func (b *Board) CellsString() string {
	out := make([]byte, Size*Size)
	for i, c := range b.Cells {
		switch c {
		case Black:
			out[i] = 'b'
		case White:
			out[i] = 'w'
		default:
			out[i] = '.'
		}
	}
	return string(out)
}
