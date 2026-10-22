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

func TestFullGroupEventRoutesSelfMentionAndSkipsBots(t *testing.T) {
	g := testGateway()
	g.botID = "BOT"
	var got []InboundMessage
	g.onMessage = func(m InboundMessage) { got = append(got, m) }
	for _, payload := range []string{
		`{"id":"1","group_openid":"G","author":{"member_openid":"A"},"content":"闲聊"}`,
		`{"id":"2","group_openid":"G","author":{"member_openid":"B"},"content":"<@LOCAL> /auto on","mentions":[{"id":"LOCAL","is_you":true}]}`,
		`{"id":"3","group_openid":"G","author":{"member_openid":"B"},"content":"<@OTHER> hi","mentions":[{"id":"OTHER"}]}`,
		`{"id":"4","group_openid":"G","author":{"member_openid":"B","bot":true},"content":"echo"}`,
		`{"id":"5","group_openid":"G","author":{"member_openid":"A"},"content":"<@!BOT> /help"}`,
	} {
		g.dispatch("GROUP_MESSAGE_CREATE", "event", mustRaw(t, payload))
	}
	if len(got) != 4 || got[0].Kind != "group" || got[1].Kind != "group_at" || got[1].Text != "/auto on" || !got[2].HasOtherMention || got[3].Text != "/help" {
		t.Fatal("full message routing failed", got)
	}
}

func TestAutoSwitchPermissionsScopeCancellationAndNoTools(t *testing.T) {
	var calls atomic.Int32
	c, st := newInputTestChannel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Tools    []any            `json:"tools"`
			Effort   string           `json:"reasoning_effort"`
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if len(input.Tools) != 0 || input.Effort != "high" {
			t.Error("optional turn gained tools or lost effort")
		}
		calls.Add(1)
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"[[QQ_SILENT]]"},"finish_reason":"stop"}]}`)
	}))
	c.cfg.AdminOpenIDs = []string{"ADMIN"}
	t.Cleanup(c.Stop)
	send := func(id, who, group, kind, text string) {
		c.onMessage(InboundMessage{Kind: kind, MsgID: id, GroupOpenID: group, MemberOpenID: who, Text: text})
	}
	send("off-input", "A", "G", "group", "disabled ordinary message")
	send("deny", "A", "G", "group_at", "/auto on")
	if c.autoEnabled(context.Background(), "qq:group:G") {
		t.Fatal("nonadmin enabled auto")
	}
	send("enable", "ADMIN", "G", "group_at", "/auto on")
	if !c.autoEnabled(context.Background(), "qq:group:G") || c.autoEnabled(context.Background(), "qq:group:OTHER") {
		t.Fatal("switch scope incorrect")
	}
	for i := 0; i < 3; i++ {
		send(fmt.Sprint("in", i), fmt.Sprint("person", i), "G", "group", fmt.Sprint("ordinary discussion ", i))
	}
	c.routingMu.Lock()
	g := c.autoGroups["qq:group:G"]
	if g.timer == nil {
		c.routingMu.Unlock()
		t.Fatal("quiet period not scheduled")
	}
	g.timer.Stop()
	rows, _ := st.RecentMessages(context.Background(), "qq:group:G", 20)
	trigger := rows[len(rows)-1].ID
	c.flushAuto("qq:group:G", "qq:group:G", "group:G:in2", trigger)
	c.routingMu.Unlock()
	waitInputTasks(t, c)
	if calls.Load() != 1 {
		t.Fatal("auto did not run tool-free once", calls.Load())
	}
	rows, _ = st.RecentMessages(context.Background(), "qq:group:G", 30)
	for _, row := range rows {
		if row.AuthorKind == "agent" || strings.Contains(row.Text, "QQ_SILENT") || strings.Contains(row.Text, "disabled ordinary") {
			t.Fatal("silence or disabled traffic saved as reply", row.Text)
		}
	}
	// Cooldown and hourly attempts must each skip.
	c.routingMu.Lock()
	c.flushAuto("qq:group:G", "qq:group:G", "group:G:in2", trigger)
	if calls.Load() != 1 {
		c.routingMu.Unlock()
		t.Fatal("cooldown bypassed")
	}
	g.lastAttempt = time.Time{}
	g.attempts = make([]time.Time, autoHourlyAttempts)
	for i := range g.attempts {
		g.attempts[i] = time.Now()
	}
	c.flushAuto("qq:group:G", "qq:group:G", "group:G:in2", trigger)
	if running, waiting := c.ag.QueueStatus(); running+waiting != 0 || !g.lastAttempt.IsZero() {
		c.routingMu.Unlock()
		t.Fatal("hourly limit submitted a turn")
	}
	c.routingMu.Unlock()
	send("next1", "A", "G", "group", "next one")
	send("next2", "B", "G", "group", "next two")
	send("next3", "C", "G", "group", "next three")
	send("new", "ADMIN", "G", "group_at", "/new")
	c.routingMu.Lock()
	timer := g.timer
	c.routingMu.Unlock()
	if timer != nil {
		t.Fatal("conversation switch left stale timer")
	}
	send("disable", "ADMIN", "G", "group_at", "/auto off")
	if c.autoEnabled(context.Background(), "qq:group:G") {
		t.Fatal("off did not persist")
	}
	if calls.Load() != 1 {
		t.Fatal("control commands or throttled turns called model")
	}
}

func TestAutoQuietTimerHonorsBusyGlobalQueue(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c, st := newInputTestChannel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(c.Stop)
	c.onMessage(InboundMessage{Kind: "c2c", UserOpenID: "U", MsgID: "busy", Text: "explicit task"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("task not started")
	}
	ctx := context.Background()
	if err := st.SetMeta(ctx, "qq:auto:qq:group:G", "on"); err != nil {
		t.Fatal(err)
	}
	sendBatch := func(prefix string) {
		for i := 0; i < 3; i++ {
			c.onMessage(InboundMessage{Kind: "group", GroupOpenID: "G", MemberOpenID: "A", MsgID: fmt.Sprint(prefix, i), Text: "chat"})
		}
	}
	sendBatch("busy-input")
	c.routingMu.Lock()
	g := c.autoGroups["qq:group:G"]
	g.timer.Stop()
	rows, _ := st.RecentMessages(ctx, "qq:group:G", 10)
	c.flushAuto("qq:group:G", "qq:group:G", "group:G:busy-input2", rows[len(rows)-1].ID)
	if !g.lastAttempt.IsZero() {
		c.routingMu.Unlock()
		t.Fatal("optional chat queued behind explicit task")
	}
	c.routingMu.Unlock()
	close(release)
	waitInputTasks(t, c)
	sendBatch("idle-input")
	deadline := time.Now().Add(autoQuiet + 2*time.Second)
	for time.Now().Before(deadline) && calls.Load() != 2 {
		time.Sleep(20 * time.Millisecond)
	}
	waitInputTasks(t, c)
	if calls.Load() != 2 {
		t.Fatal("quiet timer failed to submit optional turn")
	}
}

func TestNewInputCancelsOptionalGenerationWithoutErrorReply(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	c, st := newInputTestChannel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"stale reply"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(c.Stop)
	ctx := context.Background()
	_ = st.SetMeta(ctx, "qq:auto:qq:group:G", "on")
	for i := 0; i < 3; i++ {
		c.onMessage(InboundMessage{Kind: "group", GroupOpenID: "G", MemberOpenID: "A", MsgID: fmt.Sprint(i), Text: "chat"})
	}
	c.routingMu.Lock()
	g := c.autoGroups["qq:group:G"]
	g.timer.Stop()
	rows, _ := st.RecentMessages(ctx, "qq:group:G", 10)
	c.flushAuto("qq:group:G", "qq:group:G", "group:G:2", rows[len(rows)-1].ID)
	c.routingMu.Unlock()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	c.onMessage(InboundMessage{Kind: "group", GroupOpenID: "G", MemberOpenID: "B", MsgID: "new", Text: "new input invalidates old chat"})
	close(release)
	waitInputTasks(t, c)
	rows, _ = st.RecentMessages(ctx, "qq:group:G", 20)
	for _, row := range rows {
		if row.AuthorKind == "agent" {
			t.Fatal("cancelled chat posted output", row.Text)
		}
	}
}
