package games

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"mineagent/internal/storage"
	"path/filepath"
	"testing"
	"time"
)

// SQLite contention must not block other rooms or mutate captured results.
func TestFinishedRunDoesNotBlockRooms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "games.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), store)
	r, err := m.Create("chess", &Player{ID: 1, Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Join(&Player{ID: 2, Name: "b"}, r.ID); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	done := make(chan error, 1)
	go func() { done <- m.Resign(1) }()
	actions := make(chan error, 1)
	go func() {
		for {
			snapshot := m.RoomSnapshot(1)
			if snapshot["status"] == string(StatusFinished) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		m.Leave(1)
		_, err := m.Create("gomoku", &Player{ID: 1, Name: "a"})
		actions <- err
	}()
	select {
	case err := <-actions:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("SQLite writer lock blocked room actions")
	}
	select {
	case err := <-done:
		t.Fatalf("resignation returned before database writer released: %v", err)
	default:
	}
	if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resignation did not finish persistence")
	}
	for user, result := range map[int64]string{1: "lose", 2: "win"} {
		runs, err := store.ListGameRuns(context.Background(), user, "chess", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) != 1 || runs[0].Result != result {
			t.Fatalf("user %d lost captured result: %+v", user, runs)
		}
	}
}
