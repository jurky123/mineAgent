package games

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestChessUndoTruncatesHistory 复现：悔棋只截断 prev/sans/ucis，pliers 仍残留，
// 导致 PromptContext 继续描述已被撤销的着法。
func TestChessUndoTruncatesHistory(t *testing.T) {
	cm := newChessMatch().(*chessMatch)
	play := func(side, from, to string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"from": from, "to": to})
		if _, err := cm.Play(side, body); err != nil {
			t.Fatalf("落子 %s%s 失败: %v", from, to, err)
		}
	}
	play("white", "e2", "e4")
	play("black", "e7", "e5")

	if _, _, _, err := cm.Undo("white"); err != nil {
		t.Fatalf("悔棋失败: %v", err)
	}
	if len(cm.plies) != len(cm.sans) || len(cm.plies) != len(cm.prev) || len(cm.plies) != len(cm.ucis) {
		t.Fatalf("悔棋后历史长度不一致: plies=%d sans=%d prev=%d ucis=%d",
			len(cm.plies), len(cm.sans), len(cm.prev), len(cm.ucis))
	}
	ctx := cm.PromptContext()
	if strings.Contains(ctx, "e7") || strings.Contains(ctx, "e5") {
		t.Fatalf("撤销后 PromptContext 仍包含被撤销的 e7e5:\n%s", ctx)
	}
	if !strings.Contains(ctx, "还没有落子") {
		t.Fatalf("撤销到开局后应提示还没有落子:\n%s", ctx)
	}
}

// TestUndoInvalidatesComment 复现：悔棋后 Comments/commenting 未清理，
// 重走到同一 ply 会命中旧解说。
func TestUndoInvalidatesComment(t *testing.T) {
	m := testManager(t)
	calls := 0
	m.SetCommentator(func(context.Context, string) (string, error) {
		calls++
		return fmt.Sprintf("解说%d", calls), nil
	})

	r, err := m.Create("gomoku", &Player{ID: 1, Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Join(&Player{ID: 2, Name: "b"}, r.ID); err != nil {
		t.Fatal(err)
	}
	mustMove := func(uid int64, pt string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"point": pt})
		if err := m.Move(uid, body); err != nil {
			t.Fatalf("落子 %s 失败: %v", pt, err)
		}
	}
	mustMove(1, "h8")
	mustMove(2, "a1")

	first, pending, err := m.RequestComment(context.Background(), 1)
	if err != nil || pending || first == "" {
		t.Fatalf("首次解说失败: text=%q pending=%v err=%v", first, pending, err)
	}

	// 黑方请求悔棋，白方同意：两步都被撤销，棋局回到开局。
	if err := m.UndoRequest(1); err != nil {
		t.Fatal(err)
	}
	if err := m.UndoRespond(2, true); err != nil {
		t.Fatal(err)
	}
	snap := m.RoomSnapshot(1)
	if moves, _ := snap["moves"].([]string); len(moves) != 0 {
		t.Fatalf("悔棋后应无着法: %v", moves)
	}
	if comments, ok := snap["comments"]; ok {
		t.Fatalf("悔棋后应清空被撤销步的解说: %v", comments)
	}

	// 重走到同一 ply，重新请求解说必须重新生成而不是返回旧文案。
	mustMove(1, "h8")
	mustMove(2, "a1")
	second, pending, err := m.RequestComment(context.Background(), 1)
	if err != nil || pending {
		t.Fatalf("重走后请求解说失败: pending=%v err=%v", pending, err)
	}
	if second == first {
		t.Fatalf("重走后仍返回旧解说: %q", second)
	}
	if calls < 2 {
		t.Fatalf("重走后应重新调用解说器，calls=%d", calls)
	}
}

// TestUndoDropsInFlightComment 复现：解说生成期间悔棋，过期结果不得写入，
// 也不得清掉悔棋后新发起任务的标记。
func TestUndoDropsInFlightComment(t *testing.T) {
	m := testManager(t)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	m.SetCommentator(func(ctx context.Context, prompt string) (string, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return "迟到的解说", nil
	})

	r, err := m.Create("gomoku", &Player{ID: 1, Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Join(&Player{ID: 2, Name: "b"}, r.ID); err != nil {
		t.Fatal(err)
	}
	for _, mv := range []struct {
		uid int64
		pt  string
	}{{1, "h8"}, {2, "a1"}} {
		body, _ := json.Marshal(map[string]string{"point": mv.pt})
		if err := m.Move(mv.uid, body); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = m.RequestComment(context.Background(), 1)
	}()
	<-started // 解说生成中

	// 生成期间悔棋
	if err := m.UndoRequest(1); err != nil {
		t.Fatal(err)
	}
	if err := m.UndoRespond(2, true); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done

	snap := m.RoomSnapshot(1)
	if comments, ok := snap["comments"]; ok {
		t.Fatalf("过期解说不应写入: %v", comments)
	}
}
