package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mineagent/internal/config"
	"mineagent/internal/session"
	"mineagent/internal/storage"
)

type fakeChannel struct {
	mu   sync.Mutex
	msgs []storage.Message
}

func (f *fakeChannel) Name() string { return "minecraft" }

func (f *fakeChannel) Send(_ context.Context, msg storage.Message) error {
	f.mu.Lock()
	f.msgs = append(f.msgs, msg)
	f.mu.Unlock()
	return nil
}

func (f *fakeChannel) last() (storage.Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.msgs) == 0 {
		return storage.Message{}, false
	}
	return f.msgs[len(f.msgs)-1], true
}

func completionServer(t *testing.T, text string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"messages"`) {
			t.Errorf("unexpected request body: %s", body)
		}
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "MineAgent/") {
			t.Errorf("user-agent = %q", ua)
		}
		if sid := r.Header.Get("x-opencode-session"); sid != "mineagent-minecraft-main" {
			t.Errorf("x-opencode-session = %q", sid)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"1","object":"chat.completion","created":0,"model":"test-model",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, text)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAgentResponds(t *testing.T) {
	srv := completionServer(t, "你好，我是测试助手")

	st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	cfg.Model.BaseURL = srv.URL
	cfg.Model.APIKey = "test-key"
	cfg.Model.Name = "test-model"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := session.NewHub(st, log)
	sess, err := hub.Session(ctx, cfg.Minecraft.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	ch := &fakeChannel{}
	sess.Register(ch)

	if _, err := sess.Ingest(ctx, storage.Message{
		Channel:    "minecraft",
		AuthorKind: "player",
		AuthorName: "Steve",
		Text:       "@agent 你好",
	}); err != nil {
		t.Fatal(err)
	}

	ag, err := New(ctx, cfg, st, log)
	if err != nil {
		t.Fatal(err)
	}
	if !ag.Enabled() {
		t.Fatal("agent should be enabled")
	}
	go ag.Run(ctx)

	if !ag.Submit(Request{Session: sess, Player: "Steve", Query: "你好"}) {
		t.Fatal("submit failed")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if msg, ok := ch.last(); ok && msg.AuthorKind == "agent" {
			if msg.Text != "你好，我是测试助手" {
				t.Fatalf("reply = %q", msg.Text)
			}
			if msg.Target != "Steve" {
				t.Fatalf("target = %q", msg.Target)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("agent did not reply in time")
}

func TestAgentDisabledWithoutModel(t *testing.T) {
	st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ag, err := New(context.Background(), config.Default(), st, log)
	if err != nil {
		t.Fatal(err)
	}
	if ag.Enabled() {
		t.Fatal("agent should be disabled without model config")
	}
	if ag.Submit(Request{}) {
		t.Fatal("submit should fail when disabled")
	}
}

func TestHistoryRoles(t *testing.T) {
	st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()

	hub := session.NewHub(st, log)
	sess, err := hub.Session(ctx, cfg.Minecraft.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Ingest(ctx, storage.Message{Channel: "minecraft", AuthorKind: "player", AuthorName: "Steve", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Reply(ctx, "hello", "Steve"); err != nil {
		t.Fatal(err)
	}

	ag := &Agent{store: st, log: log, sessionID: cfg.Minecraft.SessionID}
	msgs, err := ag.history(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len = %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "[Steve] hi" {
		t.Fatalf("user msg = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "hello" {
		t.Fatalf("assistant msg = %+v", msgs[1])
	}
}
