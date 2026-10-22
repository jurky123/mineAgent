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

func TestQQEffortCommandBypassesBusyQueueAndIsScoped(t *testing.T) {
	efforts := make(chan string, 5)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c, s := newInputTestChannel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Effort string `json:"reasoning_effort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		n := calls.Add(1)
		efforts <- input.Effort
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
	send := func(id, user, text string) {
		c.onMessage(InboundMessage{Kind: "c2c", MsgID: id, UserOpenID: user, Text: text})
	}
	send("first", "U", "start")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first not started")
	}
	send("set", "U", "/effort low")
	send("query", "U", "/effort")
	send("bad", "U", "/effort off")
	current, err := s.ActiveQQConversation(context.Background(), "qq:c2c:U", 1)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := s.SessionEffort(context.Background(), current.SessionID); err != nil || value != "low" {
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
	if <-efforts != "high" || <-efforts != "low" {
		t.Fatal("command changed running task or next task used wrong effort")
	}
	send("third", "V", "other person")
	waitInputTasks(t, c)
	if <-efforts != "high" {
		t.Fatal("setting leaked to another session")
	}
	send("fork", "U", "/fork")
	fork, _ := s.ActiveQQConversation(context.Background(), "qq:c2c:U", 2)
	if value, err := s.SessionEffort(context.Background(), fork.SessionID); err != nil || value != "low" {
		t.Fatal("fork did not inherit setting")
	}
	send("new", "U", "/new")
	send("reset-query", "U", "/effort")
	fresh, _ := s.ActiveQQConversation(context.Background(), "qq:c2c:U", 3)
	rows, _ := s.RecentMessages(context.Background(), fresh.SessionID, 10)
	if !strings.Contains(rows[len(rows)-1].Text, "当前沿用 高") {
		t.Fatal("new did not use default")
	}
	send("resume", "U", "/resume default")
	send("default", "U", "/effort default")
	if value, err := s.SessionEffort(context.Background(), current.SessionID); err != nil || value != "" {
		t.Fatal("default not restored")
	}
}
