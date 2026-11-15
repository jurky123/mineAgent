package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mineagent/internal/config"
	"mineagent/internal/prompts"
	"mineagent/internal/session"
	"mineagent/internal/storage"
)

func TestChannelModelSnapshotAndExplicitOverride(t *testing.T) {
	m, err := prompts.Init(filepath.Join(t.TempDir(), "prompts.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = prompts.Init(filepath.Join(t.TempDir(), "reset.json")) })
	m.Register(prompts.Service{ID: "qq", DefaultText: func() string { return "rules" }})
	if err := m.SetServiceSettings("qq", prompts.CapabilityOverride{}, "", "channel-model"); err != nil {
		t.Fatal(err)
	}
	a := &Agent{enabled: true, jobs: make(chan Request, 3)}
	a.Submit(Request{Channel: "qq"})
	a.Submit(Request{Channel: "qq", Model: "explicit"})
	a.Submit(Request{Channel: "web"})
	if err := m.SetServiceSettings("qq", prompts.CapabilityOverride{}, "", "changed"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"channel-model", "explicit", ""} {
		if got := (<-a.jobs).Model; got != want {
			t.Fatal(got, want)
		}
	}
}

func TestHaikuSummaryAndReplyIgnorePersistedEmptyAssistant(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role, Content string
				ToolCalls     []any `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		for _, m := range body.Messages {
			if m.Role == "assistant" && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
				t.Error("invalid assistant reached provider")
				http.Error(w, "empty assistant", 400)
				return
			}
		}
		if body.Model != "claude-haiku-5-5" {
			t.Error("selected model lost", body.Model)
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"fixture","choices":[{"message":{"role":"assistant","content":"fixture accepted"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sess, err := session.NewHub(st, log).Session(ctx, "qq:group:fixture")
	if err != nil {
		t.Fatal(err)
	}
	ch := &fakeChannel{}
	sess.Register(ch)
	var trigger int64
	for i := 0; i < 24; i++ {
		kind, text := "player", fmt.Sprint("synthetic user ", i)
		if i%3 == 0 {
			kind, text = "agent", " \n "
		}
		if i%3 == 1 {
			kind, text = "agent", "saved answer"
		}
		row, e := sess.Ingest(ctx, storage.Message{Channel: "qq", AuthorKind: kind, AuthorName: "Fixture", Text: text})
		if e != nil {
			t.Fatal(e)
		}
		trigger = row.ID
	}
	cfg := config.Default()
	cfg.Model.BaseURL = srv.URL
	cfg.Model.Name = "fixture-default"
	cfg.Model.APIKey = "fixture"
	cfg.Agent.SummaryMessages = 4
	cfg.Agent.SummaryTokens = 1000
	a, err := New(ctx, cfg, st, log, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := a.runnerFor(ctx, Request{Model: "claude-haiku-5-5", SessionKey: cfg.Minecraft.SessionID})
	if err != nil || r == a.mcRunner {
		t.Fatal("explicit model ignored by fast path", err)
	}
	messages, _, err := a.history("qq:group:fixture", trigger)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range messages {
		if m.Role == "assistant" && strings.TrimSpace(m.Content) == "" {
			t.Fatal("empty history retained")
		}
	}
	rows, _ := st.RecentMessages(ctx, "qq:group:fixture", 100)
	if len(rows) != 24 {
		t.Fatal("history was deleted")
	}
	go a.Run(ctx)
	done := make(chan struct{})
	if !a.Submit(Request{Session: sess, SessionKey: "qq:group:fixture", Channel: "qq", Model: "claude-haiku-5-5", Query: "continue", TriggerMessageID: trigger, SystemInstruction: "fixture QQ rules", Done: func() { close(done) }}) {
		t.Fatal("not submitted")
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("request did not finish")
	}
	if calls.Load() < 2 {
		t.Fatal("summary and reply not both called", calls.Load())
	}
	if reply, ok := ch.last(); !ok || reply.Text != "fixture accepted" {
		t.Fatal("reply failed", reply.Text)
	}
	if summary, e := st.LatestSummary(ctx, "qq:group:fixture"); e != nil || summary == nil {
		t.Fatal("summary not saved", e)
	}
}
