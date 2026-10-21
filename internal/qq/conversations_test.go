package qq

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mineagent/internal/storage"
)

func TestQQConversationCommandsBypassAgentAndKeepHistory(t *testing.T) {
	var calls atomic.Int32
	c, s := newInputTestChannel(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	ctx := context.Background()
	scope := "qq:c2c:U"
	original, err := s.ActiveQQConversation(ctx, scope, 1)
	if err != nil {
		t.Fatal(err)
	}
	s.AppendMessage(ctx, storage.Message{SessionID: scope, Channel: "qq", AuthorKind: "player", Text: "remember me", CreatedAt: 2})
	// Keep a task running: commands must neither wait for it nor discard it.
	task := c.newTask(scope, "U", "c2c:U:old", scope)
	send := func(id, text string) {
		t.Helper()
		c.onMessage(InboundMessage{Kind: "c2c", UserOpenID: "U", MsgID: id, Text: text})
	}
	send("help", "/help")
	send("fork", "/fork")
	fork, err := s.ActiveQQConversation(ctx, scope, 3)
	if err != nil || fork.Conv == original.Conv {
		t.Fatal("fork did not switch", err)
	}
	rows, _ := s.RecentMessages(ctx, fork.SessionID, 20)
	if len(rows) < 2 || rows[0].Text != "remember me" {
		t.Fatal("fork lost history")
	}
	send("new", "/new")
	fresh, _ := s.ActiveQQConversation(ctx, scope, 4)
	rows, _ = s.RecentMessages(ctx, fresh.SessionID, 20)
	for _, r := range rows {
		if r.AuthorKind == "player" || r.AuthorKind == "agent" {
			t.Fatal("new has model context")
		}
	}
	send("list", "/resume")
	rows, _ = s.RecentMessages(ctx, fresh.SessionID, 20)
	if !strings.Contains(rows[len(rows)-1].Text, fork.Conv) {
		t.Fatal("list lacks fork")
	}
	send("restore", "/resume default")
	active, _ := s.ActiveQQConversation(ctx, scope, 5)
	if active.Conv != "default" {
		t.Fatal("resume failed")
	}
	send("invalid", "/new extra")
	active, _ = s.ActiveQQConversation(ctx, scope, 6)
	if active.Conv != "default" {
		t.Fatal("bad args changed pointer")
	}
	other, _ := s.NewQQConversation(ctx, "qq:c2c:V", false, 7)
	send("foreign", "/resume "+other.Conv)
	active, _ = s.ActiveQQConversation(ctx, scope, 8)
	if active.Conv != "default" {
		t.Fatal("resumed another user")
	}
	if calls.Load() != 0 {
		t.Fatalf("commands invoked model %d times", calls.Load())
	}
	select {
	case <-task.ctx.Done():
		t.Fatal("switch cancelled old task")
	default:
	}
	send("stop", "/stop")
	if task.ctx.Err() == nil {
		t.Fatal("stop lost archived task")
	}
	task.cancel()
	c.finishTask(scope, task)
}
func TestQQGroupResumeDoesNotReadOtherGroup(t *testing.T) {
	c, s := newInputTestChannel(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected model call") }))
	other, _ := s.NewQQConversation(context.Background(), "qq:group:OTHER", false, time.Now().UnixMilli())
	c.onMessage(InboundMessage{Kind: "group_at", GroupOpenID: "GROUP", MemberOpenID: "member", MsgID: "r", Text: "/resume " + other.Conv})
	current, _ := s.ActiveQQConversation(context.Background(), "qq:group:GROUP", 1)
	if current.Conv != "default" {
		t.Fatal("group leaked")
	}
}
