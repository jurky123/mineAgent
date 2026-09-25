package chess

import "testing"

// perft 统计某层数的合法走法数（标准测试：1=20 2=400 3=8902）。
func perft(b *Board, depth int) int {
	if depth == 0 {
		return 1
	}
	n := 0
	for _, m := range b.LegalMoves() {
		nb := b.Clone()
		nb.apply(m)
		n += perft(nb, depth-1)
	}
	return n
}

func TestPerft(t *testing.T) {
	cases := []struct {
		depth, want int
	}{
		{1, 20}, {2, 400}, {3, 8902},
	}
	for _, c := range cases {
		if got := perft(Start(), c.depth); got != c.want {
			t.Errorf("perft(%d) = %d，want %d", c.depth, got, c.want)
		}
	}
}

func mustFEN(t *testing.T, fen string) *Board {
	t.Helper()
	b, err := ParseFEN(fen)
	if err != nil {
		t.Fatalf("FEN 解析失败 %q: %v", fen, err)
	}
	return b
}

func TestFoolsMate(t *testing.T) {
	b := Start()
	for _, mv := range []string{"f2f3", "e7e5", "g2g4"} {
		m, _ := ParseMove(mv)
		if _, err := b.Play(m.From, m.To, m.Promotion); err != nil {
			t.Fatalf("%s 非法: %v", mv, err)
		}
	}
	// 1.f3 e5 2.g4 Qh4#
	m, _ := ParseMove("d8h4")
	res, err := b.Play(m.From, m.To, 0)
	if err != nil {
		t.Fatalf("后杀失败: %v", err)
	}
	if !res.Check || !res.Checkmate {
		t.Fatalf("应是将杀，得到 check=%v mate=%v", res.Check, res.Checkmate)
	}
}

func TestStalemate(t *testing.T) {
	// 黑王 a8，白后 c7、白王 c6 -> 黑无子可动且未被将军（逼和）。
	b := mustFEN(t, "k7/2Q5/2K5/8/8/8/8/8 b - - 0 1")
	if b.InCheck(Black) {
		t.Fatal("黑不应被将军")
	}
	if got := len(b.LegalMoves()); got != 0 {
		t.Fatalf("黑应无合法走法，得到 %d", got)
	}
}

func TestPinIsIllegal(t *testing.T) {
	// 白王 e1，白车 e2 挡着黑车 e8：白车只能沿 e 线走。
	b := mustFEN(t, "4r3/8/8/8/8/8/4R3/4K3 w - - 0 1")
	m, _ := ParseMove("e2d2")
	if _, err := b.Play(m.From, m.To, 0); err == nil {
		t.Fatal("被牵制的车横走应非法")
	}
	m, _ = ParseMove("e2e7")
	if _, err := b.Play(m.From, m.To, 0); err != nil {
		t.Fatalf("被牵制的车沿线走应合法: %v", err)
	}
}

func TestCastling(t *testing.T) {
	// 白可短易位：f1/g1 空且不被攻击。
	b := mustFEN(t, "r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1")
	paths := map[string]bool{}
	for _, m := range b.LegalMoves() {
		paths[m.String()] = true
	}
	if !paths["e1g1"] || !paths["e1c1"] {
		t.Fatalf("白应可短/长易位: %v", paths)
	}
	m, _ := ParseMove("e1g1")
	if _, err := b.Play(m.From, m.To, 0); err != nil {
		t.Fatalf("短易位失败: %v", err)
	}
	if b.Squares[5] != 'R' || b.Squares[7] != 0 || b.Squares[6] != 'K' {
		t.Fatalf("易位后布局不对: %s", b.FEN())
	}
	// 穿过被攻击格：黑象 a4 控制 d1 -> 白不可长易位（e1 未被将军）。
	b2 := mustFEN(t, "r3k2r/8/8/8/b7/8/8/R3K2R w KQkq - 0 1")
	for _, m := range b2.LegalMoves() {
		if m.String() == "e1c1" {
			t.Fatal("王经过被攻击格，长易位应非法")
		}
	}
	// 王动过后失去权利
	b3 := mustFEN(t, "r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1")
	mv, _ := ParseMove("e1e2")
	if _, err := b3.Play(mv.From, mv.To, 0); err != nil {
		t.Fatal(err)
	}
	mv2, _ := ParseMove("e8e7")
	if _, err := b3.Play(mv2.From, mv2.To, 0); err != nil {
		t.Fatal(err)
	}
	mv3, _ := ParseMove("e2e1")
	if _, err := b3.Play(mv3.From, mv3.To, 0); err != nil {
		t.Fatal(err)
	}
	for _, m := range b3.LegalMoves() {
		if m.String() == "e1g1" {
			t.Fatal("王回到 e1 也不该恢复易位权利")
		}
	}
}

func TestPromotion(t *testing.T) {
	b := mustFEN(t, "8/4P3/8/8/8/8/8/K6k w - - 0 1")
	m, _ := ParseMove("e7e8q")
	res, err := b.Play(m.From, m.To, 'q')
	if err != nil {
		t.Fatalf("升变失败: %v", err)
	}
	if b.Squares[56+4] != 'Q' || b.Squares[52] != 0 {
		t.Fatalf("升变结果不对: %s", b.FEN())
	}
	if res.Promotion != 'q' {
		t.Fatalf("Promotion = %q", res.Promotion)
	}
	// 指定升变为马
	b2 := mustFEN(t, "8/4P3/8/8/8/8/8/K6k w - - 0 1")
	m2, _ := ParseMove("e7e8n")
	if _, err := b2.Play(m2.From, m2.To, 'n'); err != nil {
		t.Fatalf("升变为马失败: %v", err)
	}
	if b2.Squares[56+4] != 'N' {
		t.Fatalf("应为马: %s", b2.FEN())
	}
}

func TestKingCannotMoveIntoCheck(t *testing.T) {
	b := mustFEN(t, "4r3/8/8/8/8/8/8/4K3 w - - 0 1")
	moves := b.LegalMoves()
	if len(moves) == 0 {
		t.Fatal("王应有走法")
	}
	for _, m := range moves {
		if m.To == 12 { // e2 仍在 e 线上，被 e8 车控制
			t.Fatalf("王不能走到 e2（被车控制）：%s", m)
		}
	}
}

func TestCheckmateAndStalemateFlags(t *testing.T) {
	// 白车底线将杀（黑王 h8，白车 a8 无路）。
	b := mustFEN(t, "6k1/5ppp/8/8/8/8/8/R6K w - - 0 1")
	m, _ := ParseMove("a1a8")
	res, err := b.Play(m.From, m.To, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Check || !res.Checkmate {
		t.Fatalf("a8 应是杀: check=%v mate=%v", res.Check, res.Checkmate)
	}
}
