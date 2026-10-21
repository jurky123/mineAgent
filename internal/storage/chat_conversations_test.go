package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestQQConversationForkResumeIsolationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scope := "qq:c2c:one"
	original, err := s.ActiveQQConversation(ctx, scope, 1)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := s.AppendMessage(ctx, Message{SessionID: original.SessionID, AuthorKind: "player", Channel: "qq", Text: "original question", CreatedAt: 2})
	// Interleaved rows and a removed row ensure new ids are not offset by source ids.
	s.AppendMessage(ctx, Message{SessionID: "unrelated", AuthorKind: "player", Text: "secret", CreatedAt: 3})
	second, _ := s.AppendMessage(ctx, Message{SessionID: original.SessionID, AuthorKind: "agent", Text: "original answer", CreatedAt: 4})
	s.SaveSummary(ctx, original.SessionID, first, "first summary", 4)
	s.SaveSummary(ctx, original.SessionID, second, "latest summary", 5)
	fork, err := s.NewQQConversation(ctx, scope, true, 6)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.RecentMessages(ctx, fork.SessionID, 20)
	if err != nil || len(rows) != 2 || rows[0].Text != "original question" || rows[1].Text != "original answer" {
		t.Fatalf("fork: %+v %v", rows, err)
	}
	summary, err := s.LatestSummary(ctx, fork.SessionID)
	if err != nil || summary.UpToMessageID != rows[1].ID || summary.Text != "latest summary" {
		t.Fatalf("summary: %+v %v", summary, err)
	}
	earlier, err := s.SummaryAtOrBefore(ctx, fork.SessionID, rows[0].ID)
	if err != nil || earlier == nil || earlier.Text != "first summary" {
		t.Fatalf("earlier summary: %+v %v", earlier, err)
	}
	s.AppendMessage(ctx, Message{SessionID: original.SessionID, AuthorKind: "agent", Text: "late original reply", CreatedAt: 7})
	s.AppendMessage(ctx, Message{SessionID: fork.SessionID, AuthorKind: "player", Text: "fork question", CreatedAt: 8})
	rows, _ = s.RecentMessages(ctx, fork.SessionID, 20)
	if len(rows) != 3 || rows[2].Text != "fork question" {
		t.Fatal("fork kept following source")
	}
	fresh, err := s.NewQQConversation(ctx, scope, false, 9)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := s.CountMessages(ctx, fresh.SessionID)
	sum, _ := s.LatestSummary(ctx, fresh.SessionID)
	if n != 0 || sum != nil {
		t.Fatal("new inherited context")
	}
	if _, err = s.ResumeQQConversation(ctx, "qq:group:one", fork.Conv, 10); !errors.Is(err, ErrConversationNotFound) {
		t.Fatal("cross-scope resume permitted", err)
	}
	if _, err = s.ResumeQQConversation(ctx, scope, fork.Conv, 11); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	active, err := s.ActiveQQConversation(ctx, scope, 12)
	if err != nil || active.Conv != fork.Conv {
		t.Fatal("active pointer lost on restart", err)
	}
	rows, _ = s.RecentMessages(ctx, original.SessionID, 20)
	if len(rows) != 3 || rows[2].Text != "late original reply" {
		t.Fatal("original history changed")
	}
}
func TestWebForkChecksOwnerAndRollsBack(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.CreateWebConversation(ctx, 1, "alice", "source01", "source", nil, 1); err != nil {
		t.Fatal(err)
	}
	s.AppendMessage(ctx, Message{SessionID: "web:user:1:source01", AuthorKind: "player", Text: "private", CreatedAt: 1})
	source := "source01"
	if err := s.CreateWebConversation(ctx, 2, "bob", "badfork1", "fork", &source, 2); !errors.Is(err, ErrConversationNotFound) {
		t.Fatal("foreign fork", err)
	}
	if conv, _ := s.ConversationFor(ctx, 2, "bob", "badfork1"); conv != nil {
		t.Fatal("failed fork left row")
	}
	if err := s.CreateWebConversation(ctx, 1, "alice", "fork0001", "fork", &source, 3); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.RecentMessages(ctx, "web:user:1:fork0001", 10)
	if len(rows) != 1 || rows[0].Text != "private" {
		t.Fatal("web fork lost history")
	}
}
