package gomoku

import "testing"

func place(t *testing.T, b *Board, pts ...string) {
	t.Helper()
	for _, p := range pts {
		row, col, ok := ParsePoint(p)
		if !ok {
			t.Fatalf("坐标解析失败 %q", p)
		}
		if _, _, err := b.Place(row, col); err != nil {
			t.Fatalf("落子 %s 失败: %v", p, err)
		}
	}
}

func TestParseAndName(t *testing.T) {
	cases := []struct {
		in       string
		row, col int
	}{
		{"a1", 0, 0},
		{"o15", 14, 14},
		{"h8", 7, 7},
		{"A1", 0, 0},
		{" p15 ", 14, 15 - 15}, // p 列越界 -> 下面断言
	}
	for _, c := range cases[:4] {
		row, col, ok := ParsePoint(c.in)
		if !ok || row != c.row || col != c.col {
			t.Fatalf("ParsePoint(%q) = %d,%d,%v，want %d,%d", c.in, row, col, ok, c.row, c.col)
		}
	}
	for _, bad := range []string{"", "a", "p1", "a0", "a16", "z5", "a-1"} {
		if _, _, ok := ParsePoint(bad); ok {
			t.Fatalf("ParsePoint(%q) 应失败", bad)
		}
	}
	if PointName(7, 7) != "h8" || PointName(14, 14) != "o15" {
		t.Fatalf("PointName 不对: %s %s", PointName(7, 7), PointName(14, 14))
	}
}

func TestHorizontalWin(t *testing.T) {
	b := New()
	// 黑：e5 f5 g5 h5 i5（中间穿插白子）
	place(t, b, "e5", "a1", "f5", "a2", "g5", "a3", "h5", "a4")
	if b.TurnSide() != "black" {
		t.Fatal("应轮到黑")
	}
	row, col, _ := ParsePoint("i5")
	win, line, err := b.Place(row, col)
	if err != nil || !win || len(line) != 5 {
		t.Fatalf("五连应判胜: win=%v line=%v err=%v", win, line, err)
	}
	if b.CellsString()[row*Size+col] != 'b' {
		t.Fatal("落子颜色不对")
	}
}

func TestVerticalAndDiagonal(t *testing.T) {
	b := New()
	place(t, b, "a1", "o1", "a2", "o2", "a3", "o3", "a4", "o4")
	row, col, _ := ParsePoint("a5")
	if win, line, _ := b.Place(row, col); !win || len(line) != 5 {
		t.Fatalf("竖向五连应判胜: %v", line)
	}
	// 对角线（白）
	b2 := New()
	place(t, b2, "a1", "b1", "a15", "c1", "b15", "d1", "c15", "e1", "d15", "f1")
	// 轮到白：f1 已下，改走 g1 完成 o1.. 不构成；这里直接构造另一局
	b3 := New()
	place(t, b3, "h1", "a1", "h2", "a2", "h3", "a3", "h4", "a4", "o1")
	row, col, _ = ParsePoint("a5")
	if win, _, _ := b3.Place(row, col); !win {
		t.Fatal("白方竖向五连应判胜")
	}
	b4 := New()
	place(t, b4, "a1", "h1", "b2", "h2", "c3", "h3", "d4", "h4")
	row, col, _ = ParsePoint("e5")
	if win, line, _ := b4.Place(row, col); !win || len(line) != 5 {
		t.Fatalf("黑方斜向五连应判胜: %v", line)
	}
}

func TestOverlineCountsAsWin(t *testing.T) {
	b := New()
	// a,b,c,e,f 先摆上（不构成五连），最后补 d 形成 6 连
	place(t, b, "a1", "o1", "b1", "o3", "c1", "o5", "e1", "o7", "f1", "o9")
	row, col, _ := ParsePoint("d1")
	if win, line, _ := b.Place(row, col); !win || len(line) < 6 {
		t.Fatalf("六连应判胜且连线 >=6: %v", line)
	}
}

func TestOccupiedAndRange(t *testing.T) {
	b := New()
	row, col, _ := ParsePoint("h8")
	if _, _, err := b.Place(row, col); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Place(row, col); err != ErrOccupied {
		t.Fatalf("重复落子应报占用: %v", err)
	}
	if _, _, err := b.Place(-1, 0); err != ErrOutOfRange {
		t.Fatalf("越界应报错: %v", err)
	}
	if b.TurnSide() != "white" {
		t.Fatal("下完一步应轮到白")
	}
}

func TestFullCountsAsDraw(t *testing.T) {
	b := New()
	b.Moves = Size * Size
	if !b.Full() {
		t.Fatal("满盘应为和棋")
	}
}
