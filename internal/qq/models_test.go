package qq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestQQModelCommandBypassesBusyQueueAndIsScoped(t *testing.T) {
	models := make(chan string, 5)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c, s := newInputTestChannel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		n := calls.Add(1)
		models <- input.Model
		if n == 1 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		fmt.Fprint(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	c.WithModelCatalog(func(context.Context) []string { return []string{"fixture", "fixture-fast"} })
	send := func(id, user, text string) {
		c.onMessage(InboundMessage{Kind: "c2c", MsgID: id, UserOpenID: user, Text: text})
	}
	send("first", "U", "start")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first not started")
	}
	send("set", "U", "/model fixture-fast")
	send("query", "U", "/model")
	send("bad", "U", "/model unknown")
	current, err := s.ActiveQQConversation(context.Background(), "qq:c2c:U", 1)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := s.SessionModel(context.Background(), current.SessionID); err != nil || value != "fixture-fast" {
		t.Fatal("busy command failed", value, err)
	}
	c.mu.Lock()
	queued := len(c.tasks["qq:c2c:U"])
	c.mu.Unlock()
	if queued != 1 {
		t.Fatal("command entered model queue")
	}
	send("second", "U", "followup")
	close(release)
	waitInputTasks(t, c)
	if <-models != "fixture" || <-models != "fixture-fast" {
		t.Fatal("command changed running task or next task used wrong model")
	}
	send("third", "V", "other person")
	waitInputTasks(t, c)
	if <-models != "fixture" {
		t.Fatal("setting leaked to another session")
	}
	send("fork", "U", "/fork")
	fork, _ := s.ActiveQQConversation(context.Background(), "qq:c2c:U", 2)
	if value, err := s.SessionModel(context.Background(), fork.SessionID); err != nil || value != "fixture-fast" {
		t.Fatal("fork did not inherit setting")
	}
	send("new", "U", "/new")
	send("reset-query", "U", "/model")
	fresh, _ := s.ActiveQQConversation(context.Background(), "qq:c2c:U", 3)
	rows, _ := s.RecentMessages(context.Background(), fresh.SessionID, 10)
	if !strings.Contains(rows[len(rows)-1].Text, "默认（fixture）") {
		t.Fatal("new did not use default")
	}
	send("resume", "U", "/resume default")
	send("default", "U", "/model default")
	if value, err := s.SessionModel(context.Background(), current.SessionID); err != nil || value != "" {
		t.Fatal("default not restored")
	}
}

func TestQQModelSettingsCatalogAndGroupIsolation(t *testing.T) {
	c, s := newInputTestChannel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("settings called model") }))
	c.WithModelCatalog(func(context.Context) []string { return []string{"fixture", "extra/vendor/model", "fixture"} })
	ctx := context.Background()
	group, _ := s.ActiveQQConversation(ctx, "qq:group:G", 1)
	other, _ := s.ActiveQQConversation(ctx, "qq:group:H", 1)
	for _, arg := range []string{"unknown", "list 0", "list nope", "list 1 extra", "fixture extra"} {
		if _, err := c.modelSetting(ctx, group.SessionID, "model", arg); err != nil {
			t.Fatal(err)
		}
		if v, _ := s.SessionModel(ctx, group.SessionID); v != "" {
			t.Fatal("invalid selection saved", arg)
		}
	}
	if reply, err := c.modelSetting(ctx, group.SessionID, "model", "list"); err != nil || strings.Count(reply, "• fixture\n") != 1 || !strings.Contains(reply, "extra/vendor/model") {
		t.Fatal(reply, err)
	}
	c.onMessage(InboundMessage{Kind: "group_at", MsgID: "set", GroupOpenID: "G", MemberOpenID: "U", Text: "/model extra/vendor/model"})
	if v, _ := s.SessionModel(ctx, group.SessionID); v != "extra/vendor/model" {
		t.Fatal("group setting not saved", v)
	}
	if v, _ := s.SessionModel(ctx, other.SessionID); v != "" {
		t.Fatal("group setting leaked")
	}
	c.onMessage(InboundMessage{Kind: "group_at", MsgID: "settings", GroupOpenID: "G", MemberOpenID: "U", Text: "/设置"})
	rows, _ := s.RecentMessages(ctx, group.SessionID, 10)
	reply := rows[len(rows)-1].Text
	if !strings.Contains(reply, "extra/vendor/model") || !strings.Contains(reply, "/effort") || !strings.Contains(reply, "共享对话") {
		t.Fatal(reply)
	}
	c.onMessage(InboundMessage{Kind: "c2c", MsgID: "help", UserOpenID: "V", Text: "/help"})
	private, _ := s.ActiveQQConversation(ctx, "qq:c2c:V", 1)
	rows, _ = s.RecentMessages(ctx, private.SessionID, 10)
	if !strings.Contains(rows[len(rows)-1].Text, "/settings") {
		t.Fatal("missing help entry")
	}
	if cmd, arg := matchModelCommand("/MODEL extra/vendor/model"); cmd != "model" || arg != "extra/vendor/model" {
		t.Fatal("parser")
	}
}
