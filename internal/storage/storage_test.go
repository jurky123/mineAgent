package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMessages(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.EnsureSession(ctx, "main", 1000); err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{"one", "two", "three"} {
		id, err := s.AppendMessage(ctx, Message{
			SessionID:  "main",
			Channel:    "minecraft",
			AuthorKind: "player",
			AuthorName: "Steve",
			Text:       text,
			CreatedAt:  int64(1000 + i),
		})
		if err != nil {
			t.Fatal(err)
		}
		if id != int64(i+1) {
			t.Fatalf("id = %d, want %d", id, i+1)
		}
	}

	recent, err := s.RecentMessages(ctx, "main", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].Text != "two" || recent[1].Text != "three" {
		t.Fatalf("recent = %+v", recent)
	}

	after, err := s.MessagesAfter(ctx, "main", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || after[0].Text != "two" {
		t.Fatalf("after = %+v", after)
	}

	n, err := s.CountMessages(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("count = %d", n)
	}
}

func TestSummaries(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.EnsureSession(ctx, "main", 1); err != nil {
		t.Fatal(err)
	}

	sum, err := s.LatestSummary(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if sum != nil {
		t.Fatalf("expected nil summary, got %+v", sum)
	}

	if _, err := s.SaveSummary(ctx, "main", 10, "早期摘要", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSummary(ctx, "main", 20, "新摘要", 200); err != nil {
		t.Fatal(err)
	}
	sum, err = s.LatestSummary(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if sum == nil || sum.Text != "新摘要" || sum.UpToMessageID != 20 {
		t.Fatalf("latest = %+v", sum)
	}
}

func TestIdentityAndAudit(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.UpsertIdentity(ctx, "minecraft", "uuid-1", "Steve", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertIdentity(ctx, "minecraft", "uuid-1", "Steve2", 2); err != nil {
		t.Fatal(err)
	}

	id, err := s.SaveAudit(ctx, AuditEntry{
		SessionID: "main",
		CallID:    "c1",
		Tool:      "minecraft.teleport",
		Risk:      "high",
		Requester: "Steve",
		Args:      `{"player":"Steve"}`,
		Decision:  "approved",
		Operator:  "jzk",
		Result:    "ok",
		CreatedAt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Fatalf("audit id = %d", id)
	}
}
