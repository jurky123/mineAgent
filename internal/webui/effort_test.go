package webui

import (
	"context"
	"testing"
	"time"
)

func TestWebEffortCommandAndSliderSharePersistentSessionSetting(t *testing.T) {
	c, server, _ := newTestChannel(t)
	token := loginTest(t, server, "alice")
	ctx := context.Background()
	user, _ := c.store.UserByName(ctx, "alice")
	for _, conv := range []string{"source01", "other01"} {
		if err := c.store.CreateWebConversation(ctx, user.ID, user.Username, conv, "test", nil, 1); err != nil {
			t.Fatal(err)
		}
	}
	key := webSessionKey(user.ID, "source01")
	_, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.active[key] = cancel
	c.lastSend[key] = time.Now()
	c.mu.Unlock()
	command := func(conv, text string) string {
		t.Helper()
		var out struct {
			Conv    string `json:"conv"`
			Command bool   `json:"conversationCommand"`
		}
		code := doJSON(t, "POST", server.URL+"/api/send", token, map[string]any{"conv": conv, "text": text}, &out)
		if code != 200 || !out.Command {
			t.Fatal(code, out)
		}
		return out.Conv
	}
	command("source01", "/effort low")
	var options struct {
		Effort string `json:"effort"`
	}
	if code := doJSON(t, "GET", server.URL+"/api/options?conv=source01", token, nil, &options); code != 200 || options.Effort != "low" {
		t.Fatal("command not reflected in options", code, options)
	}
	p, err := c.effectivePrefs(ctx, user.Username, key)
	if err != nil || p.Effort != "low" || !p.SessionEffort {
		t.Fatal("request override missing", p, err)
	}
	if code := doJSON(t, "POST", server.URL+"/api/prefs", token, map[string]any{"conv": "source01", "effort": "high"}, nil); code != 200 {
		t.Fatal(code)
	}
	command("source01", "/effort")
	if value, err := c.store.SessionEffort(ctx, key); err != nil || value != "high" {
		t.Fatal("slider and command disagree")
	}
	if value, err := c.store.SessionEffort(ctx, webSessionKey(user.ID, "other01")); err != nil || value != "" {
		t.Fatal("setting leaked to other conversation")
	}
	fork := command("source01", "/fork")
	if value, err := c.store.SessionEffort(ctx, webSessionKey(user.ID, fork)); err != nil || value != "high" {
		t.Fatal("web fork lost setting")
	}
	fresh := command(fork, "/new")
	if value, err := c.store.SessionEffort(ctx, webSessionKey(user.ID, fresh)); err != nil || value != "" {
		t.Fatal("web new inherited setting")
	}
	command("source01", "/effort default")
	if value, err := c.store.SessionEffort(ctx, key); err != nil || value != "" {
		t.Fatal("default not restored")
	}
	foreign := loginTest(t, server, "bob")
	if code := doJSON(t, "POST", server.URL+"/api/send", foreign, map[string]any{"conv": "source01", "text": "/effort high"}, nil); code != 404 {
		t.Fatal("foreign command allowed", code)
	}
	if code := doJSON(t, "POST", server.URL+"/api/prefs", foreign, map[string]any{"conv": "source01", "effort": "high"}, nil); code != 404 {
		t.Fatal("foreign preference allowed", code)
	}
	rows, err := c.store.RecentMessages(ctx, key, 30)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.AuthorKind != "system" {
			t.Fatal("commands contaminated model context")
		}
	}
}
