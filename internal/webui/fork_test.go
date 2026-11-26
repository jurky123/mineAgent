package webui

import (
	"context"
	"testing"

	"mineagent/internal/storage"
)

func TestConversationForkAtMessage(t *testing.T) {
	c, server, _ := newTestChannel(t)
	token := loginTest(t, server, "alice")
	ctx := context.Background()
	user, err := c.store.UserByName(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.store.CreateWebConversation(ctx, user.ID, user.Username, "src001", "原始对话", nil, 1); err != nil {
		t.Fatal(err)
	}
	key := webSessionKey(user.ID, "src001")
	id1, err := c.store.AppendMessage(ctx, storage.Message{SessionID: key, Channel: "web", AuthorKind: "player", AuthorName: user.Username, Text: "第一问", CreatedAt: 10})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := c.store.AppendMessage(ctx, storage.Message{SessionID: key, Channel: "web", AuthorKind: "agent", AuthorName: "agent", Text: "第一答", CreatedAt: 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.AppendMessage(ctx, storage.Message{SessionID: key, Channel: "web", AuthorKind: "player", AuthorName: user.Username, Text: "第二问", CreatedAt: 30}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.AppendMessage(ctx, storage.Message{SessionID: key, Channel: "web", AuthorKind: "agent", AuthorName: "agent", Text: "第二答", CreatedAt: 40}); err != nil {
		t.Fatal(err)
	}

	var resp struct {
		Conv  string `json:"conv"`
		Title string `json:"title"`
	}
	if code := doJSON(t, "POST", server.URL+"/api/conversations/fork", token, map[string]any{
		"conv":      "src001",
		"messageId": id2,
	}, &resp); code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.Conv == "" || resp.Conv == "src001" {
		t.Fatalf("unexpected forked conv id: %q", resp.Conv)
	}
	rows, err := c.store.RecentMessages(ctx, webSessionKey(user.ID, resp.Conv), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Text != "第一问" || rows[1].Text != "第一答" {
		t.Fatalf("expected 2 messages up to id2=%d (after id1=%d), got %+v", id2, id1, rows)
	}
	origRows, err := c.store.RecentMessages(ctx, key, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(origRows) != 4 {
		t.Fatalf("expected original to keep 4 messages, got %d", len(origRows))
	}
	if code := doJSON(t, "POST", server.URL+"/api/conversations/fork", token, map[string]any{
		"conv":      "src001",
		"messageId": 999999,
	}, nil); code != 404 {
		t.Fatalf("expected 404 for unknown messageId, got %d", code)
	}
}
