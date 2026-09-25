package games

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"mineagent/internal/games/chess"
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

func playMove(t *testing.T, m *Manager, userID int64, mv string) {
	t.Helper()
	parsed, ok := chess.ParseMove(mv)
	if !ok {
		t.Fatalf("走法解析失败 %q", mv)
	}
	if err := m.Move(userID, parsed.From, parsed.To, parsed.Promotion); err != nil {
		t.Fatalf("走 %s 失败: %v", mv, err)
	}
}

func TestRoomFlowCheckmate(t *testing.T) {
	m := testManager(t)
	r := m.Create(&Player{ID: 1, Name: "a"})
	if r.Status != StatusWaiting {
		t.Fatalf("新建房间应为 waiting，得到 %s", r.Status)
	}
	// 房主不能加入自己的房间
	if _, err := m.Join(&Player{ID: 1, Name: "a"}, r.ID); err == nil {
		t.Fatal("房主加入自己房间应报错")
	}
	if _, err := m.Join(&Player{ID: 2, Name: "b"}, r.ID); err != nil {
		t.Fatalf("加入失败: %v", err)
	}
	// 第三方加入（房间已开局）
	if _, err := m.Join(&Player{ID: 3, Name: "c"}, r.ID); err == nil {
		t.Fatal("开局后加入应报错")
	}
	// 还没轮到黑方
	if err := m.Move(2, 52, 36, 0); err == nil {
		t.Fatal("黑方抢先走应报错")
	}
	// 1.f3 e5 2.g4 Qh4#
	for i, mv := range []string{"f2f3", "e7e5", "g2g4", "d8h4"} {
		user := int64(1)
		if i%2 == 1 {
			user = 2
		}
		playMove(t, m, user, mv)
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
	// 终局后不能再走
	if err := m.Move(1, 0, 0, 0); err == nil {
		t.Fatal("终局后走子应报错")
	}
}

func TestResignAndLeave(t *testing.T) {
	m := testManager(t)
	r := m.Create(&Player{ID: 1, Name: "a"})
	if _, err := m.Join(&Player{ID: 2, Name: "b"}, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Resign(1); err != nil {
		t.Fatal(err)
	}
	if got := m.RoomOf(1); got.Result != "black" || got.Reason != "resign" {
		t.Fatalf("认输结果不对: %+v", got)
	}
	// 双方离开后房间由 TTL 清理（这里只验证关联解除）
	m.Leave(1)
	m.Leave(2)
	if m.RoomOf(1) != nil || m.RoomOf(2) != nil {
		t.Fatal("离开后不应还在房间里")
	}
	// 等待中的房间：房主离开即删
	r2 := m.Create(&Player{ID: 3, Name: "c"})
	m.Leave(3)
	if m.RoomOf(3) != nil {
		t.Fatal("房主离开后应解散")
	}
	if len(m.OpenRooms()) != 0 {
		t.Fatal("不应有开放房间")
	}
	_ = r2
}

func TestDetachOnNewRoom(t *testing.T) {
	m := testManager(t)
	r := m.Create(&Player{ID: 1, Name: "a"})
	if _, err := m.Join(&Player{ID: 2, Name: "b"}, r.ID); err != nil {
		t.Fatal(err)
	}
	// 房主直接开新房间：旧局判对手胜
	r2 := m.Create(&Player{ID: 1, Name: "a"})
	if r2.ID == r.ID {
		t.Fatal("新房间 id 不应重复")
	}
	if got := m.RoomOf(2); got == nil || got.Status != StatusFinished || got.Result != "black" {
		t.Fatalf("旧局应判黑胜: %+v", got)
	}
}

func TestOpenRoomsAndSnapshot(t *testing.T) {
	m := testManager(t)
	r := m.Create(&Player{ID: 1, Name: "a"})
	if len(m.OpenRooms()) != 1 {
		t.Fatal("应有 1 个开放房间")
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
