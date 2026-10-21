package webui

import (
	"context"
	"fmt"
	"testing"
	"time"

	"mineagent/internal/storage"
)

func TestWebDirectConversationCommandsWhileBusyAndOwnerIsolation(t *testing.T) {
	c, server, _ := newTestChannel(t)
	token := loginTest(t, server, "alice")
	ctx := context.Background()
	user, _ := c.store.UserByName(ctx, "alice")
	if err := c.store.CreateWebConversation(ctx, user.ID, user.Username, "source01", "original", nil, 1); err != nil {
		t.Fatal(err)
	}
	key := webSessionKey(user.ID, "source01")
	m, _ := c.store.AppendMessage(ctx, storage.Message{SessionID: key, Channel: "web", AuthorKind: "player", Text: "context-marker", CreatedAt: 2})
	c.store.SaveSummary(ctx, key, m, "summary-marker", 3)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.active[key] = cancel
	c.lastSend[key] = time.Now()
	c.mu.Unlock()
	send := func(conv, text string) string {
		t.Helper()
		var out struct {
			Conv    string `json:"conv"`
			Command bool   `json:"conversationCommand"`
		}
		code := doJSON(t, "POST", server.URL+"/api/send", token, map[string]any{"conv": conv, "text": text}, &out)
		if code != 200 || !out.Command {
			t.Fatalf("%s: HTTP %d %+v", text, code, out)
		}
		return out.Conv
	}
	fork := send("source01", "/fork")
	if fork == "source01" {
		t.Fatal("fork did not switch")
	}
	rows, _ := c.store.RecentMessages(ctx, webSessionKey(user.ID, fork), 20)
	if len(rows) < 2 || rows[0].Text != "context-marker" {
		t.Fatal("fork lost context")
	}
	summary, _ := c.store.LatestSummary(ctx, webSessionKey(user.ID, fork))
	if summary == nil || summary.UpToMessageID != rows[0].ID {
		t.Fatal("summary id not remapped")
	}
	fresh := send(fork, "/new")
	rows, _ = c.store.RecentMessages(ctx, webSessionKey(user.ID, fresh), 20)
	for _, row := range rows {
		if row.AuthorKind != "system" {
			t.Fatal("new inherited model history")
		}
	}
	if send(fresh, "/resume source01") != "source01" {
		t.Fatal("resume failed")
	}
	send("source01", "/help")
	send("source01", "/resume")
	select {
	case <-runCtx.Done():
		t.Fatal("switch cancelled prior task")
	default:
	}
	other := loginTest(t, server, "bob")
	var result struct {
		Conv string `json:"conv"`
	}
	if code := doJSON(t, "POST", server.URL+"/api/send", other, map[string]any{"text": "/resume " + fork}, &result); code != 200 {
		t.Fatal(code)
	}
	if result.Conv == fork {
		t.Fatal("foreign resume")
	}
	if code := doJSON(t, "POST", server.URL+"/api/send", other, map[string]any{"conv": fork, "text": "/fork"}, nil); code != 404 {
		t.Fatal("foreign source accepted", code)
	}
	if code := doJSON(t, "POST", server.URL+"/api/send", token, map[string]any{"conv": fresh, "text": "/new", "files": []UploadedFile{{Path: fmt.Sprintf("web-files/u%d/a.txt", user.ID), Name: "a.txt", Mime: "text/plain"}}}, nil); code == 200 {
		t.Fatal("command silently discarded files")
	}
}
