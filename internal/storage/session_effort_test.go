package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSessionEffortPersistsAndFollowsConversationLifecycle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.ActiveQQConversation(ctx, "qq:group:G", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSessionEffort(ctx, current.SessionID, "low"); err != nil {
		t.Fatal(err)
	}
	fork, err := s.NewQQConversation(ctx, "qq:group:G", true, 2)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := s.SessionEffort(ctx, fork.SessionID); err != nil || value != "low" {
		t.Fatal("fork lost effort", value, err)
	}
	fresh, err := s.NewQQConversation(ctx, "qq:group:G", false, 3)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := s.SessionEffort(ctx, fresh.SessionID); err != nil || value != "" {
		t.Fatal("new inherited override", value, err)
	}
	if err = s.SetSessionEffort(ctx, fork.SessionID, "extreme"); err == nil {
		t.Fatal("invalid effort accepted")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if value, err := s.SessionEffort(ctx, current.SessionID); err != nil || value != "low" {
		t.Fatal("restart lost setting", value, err)
	}
	if err = s.SetSessionEffort(ctx, current.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	if value, err := s.SessionEffort(ctx, current.SessionID); err != nil || value != "" {
		t.Fatal("default did not clear override", value, err)
	}
	if value, err := s.SessionEffort(ctx, fork.SessionID); err != nil || value != "low" {
		t.Fatal("reset changed fork", value, err)
	}
}
