package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSessionModelPersistsAndFollowsConversationLifecycle(t *testing.T) {
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
	if err = s.SetSessionModel(ctx, current.SessionID, "provider/model"); err != nil {
		t.Fatal(err)
	}
	fork, err := s.NewQQConversation(ctx, "qq:group:G", true, 2)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := s.SessionModel(ctx, fork.SessionID); err != nil || value != "provider/model" {
		t.Fatal("fork lost model", value, err)
	}
	fresh, err := s.NewQQConversation(ctx, "qq:group:G", false, 3)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := s.SessionModel(ctx, fresh.SessionID); err != nil || value != "" {
		t.Fatal("new inherited override", value, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if value, err := s.SessionModel(ctx, current.SessionID); err != nil || value != "provider/model" {
		t.Fatal("restart lost setting", value, err)
	}
	if err = s.SetSessionModel(ctx, current.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	if value, err := s.SessionModel(ctx, current.SessionID); err != nil || value != "" {
		t.Fatal("default did not clear override", value, err)
	}
	if value, err := s.SessionModel(ctx, fork.SessionID); err != nil || value != "provider/model" {
		t.Fatal("reset changed fork", value, err)
	}
}
