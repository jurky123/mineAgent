package games

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"mineagent/internal/storage"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), store)
}

func playChess(t *testing.T, m *Manager, userID int64, mv string) {
	t.Helper()
	from, to := mv[0:2], mv[2:4]
	promo := ""
	if len(mv) == 5 {
		promo = mv[4:]
	}
	body, _ := json.Marshal(map[string]string{"from": from, "to": to, "promotion": promo})
	if err := m.Move(userID, body); err != nil {
		t.Fatalf("走 %s 失败: %v", mv, err)
	}
}

func TestChessRoomFlowCheckmate(t *testing.T) {
	m := testManager(t)
	r, err := m.Create("chess", &Player{ID: 1, Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusWaiting || r.Sides["white"] == nil || r.Sides["white"].ID != 1 {
		t.Fatalf("建房状态不对: %+v", r)
	}
	if _, err := m.Join(&Player{ID: 1, Name: "a"}, r.ID); err == nil {
		t.Fatal("房主加入自己房间应报错")
	}
	if _, err := m.Join(&Player{ID: 2, Name: "b"}, r.ID); err != nil {
		t.Fatalf("加入失败: %v", err)
	}
	if _, err := m.Join(&Player{ID: 3, Name: "c"}, r.ID); err == nil {
		t.Fatal("开局后加入应报错")
	}
	// 还没轮到黑方
	if err := m.Move(2, json.RawMessage(`{"from":"e7","to":"e5"}`)); err == nil {
		t.Fatal("黑方抢先走应报错")
	}
	for i, mv := range []string{"f2f3", "e7e5", "g2g4", "d8h4"} {
		user := int64(1)
		if i%2 == 1 {
			user = 2
		}
		playChess(t, m, user, mv)
	}
	got := m.RoomOf(1)
	if got.Status != StatusFinished || got.Result != "black" || got.Reason != "checkmate" {
		t.Fatalf("终局状态不对: status=%s result=%s reason=%s", got.Status, got.Result, got.Reason)
	}
	runs, err := m.store.ListGameRuns(context.Background(), 1, "chess", 10)
	if err != nil || len(runs) != 1 || runs[0].Result != "lose" {
		t.Fatalf("白方记录不对: %+v err=%v", runs, err)
	}
	runs2, err := m.store.ListGameRuns(context.Background(), 2, "chess", 10)
	if err != nil || len(runs2) != 1 || runs2[0].Result != "win" {
		t.Fatalf("黑方记录不对: %+v err=%v", runs2, err)
	}
	if err := m.Move(1, json.RawMessage(`{"from":"a2","to":"a3"}`)); err == nil {
		t.Fatal("终局后走子应报错")
	}
}

func TestGomokuRoomFlowWin(t *testing.T) {
	m := testManager(t)
	r, err := m.Create("gomoku", &Player{ID: 10, Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	// 五子棋房主执黑（先手）
	if r.Sides["black"] == nil || r.Sides["black"].ID != 10 {
		t.Fatalf("房主应执黑: %+v", r.Sides)
	}
	if _, err := m.Join(&Player{ID: 11, Name: "y"}, r.ID); err != nil {
		t.Fatal(err)
	}
	snap := m.RoomOf(10).Snapshot(10)
	if snap["turn"] != "black" || snap["you"] != "black" {
		t.Fatalf("开局快照不对: %+v", snap)
	}
	// 黑 h8 -> 白 a1 -> 黑 i8 -> 白 a2 -> 黑 j8 -> 白 a3 -> 黑 k8 -> 白 a4 -> 黑 l8 五连
	moves := []struct {
		user int64
		pt   string
	}{
		{10, "h8"}, {11, "a1"}, {10, "i8"}, {11, "a2"},
		{10, "j8"}, {11, "a3"}, {10, "k8"}, {11, "a4"}, {10, "l8"},
	}
	for _, mv := range moves {
		body, _ := json.Marshal(map[string]string{"point": mv.pt})
		if err := m.Move(mv.user, body); err != nil {
			t.Fatalf("落子 %s 失败: %v", mv.pt, err)
		}
	}
	got := m.RoomOf(10)
	if got.Status != StatusFinished || got.Result != "black" || got.Reason != "five" {
		t.Fatalf("终局不对: status=%s result=%s reason=%s", got.Status, got.Result, got.Reason)
	}
	if n := len(got.Moves); n != 9 {
		t.Fatalf("步数不对: %d", n)
	}
	// 获胜连线应在快照里（5 个格）
	snapWin := got.Snapshot(10)
	line, ok := snapWin["winLine"].([]int)
	if !ok || len(line) < 5 {
		t.Fatalf("缺少获胜连线: %+v", snapWin["winLine"])
	}
	runs, _ := m.store.ListGameRuns(context.Background(), 10, "gomoku", 10)
	if len(runs) != 1 || runs[0].Result != "win" {
		t.Fatalf("黑方记录不对: %+v", runs)
	}
	runsW, _ := m.store.ListGameRuns(context.Background(), 11, "gomoku", 10)
	if len(runsW) != 1 || runsW[0].Result != "lose" {
		t.Fatalf("白方记录不对: %+v", runsW)
	}
	// 不同游戏的房间不互通
	if m.RoomOf(10).GameID != "gomoku" {
		t.Fatal("房间游戏 id 不对")
	}
}

func TestResignAndLeave(t *testing.T) {
	m := testManager(t)
	r, _ := m.Create("chess", &Player{ID: 1, Name: "a"})
	if _, err := m.Join(&Player{ID: 2, Name: "b"}, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Resign(1); err != nil {
		t.Fatal(err)
	}
	if got := m.RoomOf(1); got.Result != "black" || got.Reason != "resign" {
		t.Fatalf("认输结果不对: %+v", got)
	}
	m.Leave(1)
	m.Leave(2)
	if m.RoomOf(1) != nil || m.RoomOf(2) != nil {
		t.Fatal("离开后不应还在房间里")
	}
	r2, _ := m.Create("gomoku", &Player{ID: 3, Name: "c"})
	m.Leave(3)
	if m.RoomOf(3) != nil {
		t.Fatal("房主离开后应解散")
	}
	if len(m.OpenRooms("")) != 0 {
		t.Fatal("不应有开放房间")
	}
	_ = r2
}

func TestDetachOnNewRoom(t *testing.T) {
	m := testManager(t)
	r, _ := m.Create("chess", &Player{ID: 1, Name: "a"})
	if _, err := m.Join(&Player{ID: 2, Name: "b"}, r.ID); err != nil {
		t.Fatal(err)
	}
	// 房主直接开新房间：旧局判对手胜
	r2, err := m.Create("gomoku", &Player{ID: 1, Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if r2.ID == r.ID {
		t.Fatal("新房间 id 不应重复")
	}
	if got := m.RoomOf(2); got == nil || got.Status != StatusFinished || got.Result != "black" {
		t.Fatalf("旧局应判黑胜: %+v", got)
	}
}

func TestOpenRoomsAndSnapshot(t *testing.T) {
	m := testManager(t)
	r, _ := m.Create("chess", &Player{ID: 1, Name: "a"})
	if len(m.OpenRooms("chess")) != 1 || len(m.OpenRooms("gomoku")) != 0 {
		t.Fatal("开放房间应按游戏过滤")
	}
	// 另一个游戏也能同时开一间
	if _, err := m.Create("gomoku", &Player{ID: 9, Name: "z"}); err != nil {
		t.Fatal(err)
	}
	if len(m.OpenRooms("gomoku")) != 1 {
		t.Fatal("五子棋房间应存在")
	}
	snap := r.Snapshot(1)
	if snap["status"] != StatusWaiting || snap["you"] != "white" {
		t.Fatalf("快照不对: %+v", snap)
	}
	if _, ok := snap["legalMoves"]; ok {
		t.Fatal("等待中不应下发合法走法")
	}
	if _, err := m.Join(&Player{ID: 2, Name: "b"}, r.ID); err != nil {
		t.Fatal(err)
	}
	snapW := m.RoomOf(1).Snapshot(1)
	lm, ok := snapW["legalMoves"].([]string)
	if !ok || len(lm) != 20 {
		t.Fatalf("白方开局应有 20 个合法走法，得到 %v", snapW["legalMoves"])
	}
	snapB := m.RoomOf(2).Snapshot(2)
	if _, ok := snapB["legalMoves"]; ok {
		t.Fatal("不是黑方的回合，不应下发走法")
	}
	if snapB["you"] != "black" {
		t.Fatalf("黑方视角不对: %+v", snapB)
	}
}
