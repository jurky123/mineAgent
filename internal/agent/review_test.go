package agent

import (
	"bytes"
	"context"
	"fmt"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
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
	"mineagent/internal/session"
	"mineagent/internal/storage"
)

func TestRequestCancellationStopsModelAndPreservesMessage(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer server.Close()
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	cfg.Model.BaseURL = server.URL
	cfg.Model.APIKey = "fixture"
	cfg.Model.Name = "test-model"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := session.NewHub(store, log)
	sess, err := hub.Session(ctx, cfg.Minecraft.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	ch := &fakeChannel{}
	sess.Register(ch)
	msg, err := sess.Ingest(ctx, storage.Message{AuthorKind: "player", Text: "取消测试", Channel: "minecraft"})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := New(ctx, cfg, store, log, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	go ag.Run(ctx)
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{})
	if !ag.Submit(Request{Context: runCtx, Done: func() { close(done) }, Session: sess, SessionKey: cfg.Minecraft.SessionID, TriggerMessageID: msg.ID, Query: "取消测试"}) {
		t.Fatal("submit")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("model not started")
	}
	stop()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not finish")
	}
	rows, err := store.RecentMessages(ctx, cfg.Minecraft.SessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 || rows[0].Text != "取消测试" || !strings.Contains(rows[len(rows)-1].Text, "已停止") {
		t.Fatalf("lost cancellation history: %+v", rows)
	}
}
func TestCancelPendingApprovalPreventsLateResume(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	called := false
	a := &Agent{pending: map[string]*pendingRun{"approval": {sessionKey: "own", request: Request{Context: ctx, Done: func() { called = true }}}}}
	a.CancelPending("own")
	if len(a.pending) != 1 || called {
		t.Fatal("cancelled live approval")
	}
	cancel()
	a.CancelPending("other")
	if len(a.pending) != 1 {
		t.Fatal("cancelled other session")
	}
	a.CancelPending("own")
	if len(a.pending) != 0 || !called {
		t.Fatal("pending approval still resumable")
	}
}

// A real tool invocation against a deterministic local model must leave start/return metadata.
func TestToolEventsReflectActualInvocation(t *testing.T) {
	var invoked atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if bytes.Contains(body, []byte(`"role":"tool"`)) {
			fmt.Fprint(w, `{"id":"2","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"当前时间已查询"},"finish_reason":"stop"}]}`)
		} else {
			fmt.Fprint(w, `{"id":"1","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"clock","type":"function","function":{"name":"current_time","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
		}
	}))
	defer server.Close()
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	cfg.Model.BaseURL = server.URL
	cfg.Model.APIKey = "fixture"
	cfg.Model.Name = "test-model"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hub := session.NewHub(store, log)
	sess, err := hub.Session(ctx, cfg.Minecraft.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := sess.Ingest(ctx, storage.Message{Channel: "minecraft", AuthorKind: "player", Text: "查询当前时间"})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := New(ctx, cfg, store, log, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	go ag.Run(ctx)
	done := make(chan struct{})
	var events []ToolEvent
	ag.Submit(Request{Session: sess, SessionKey: cfg.Minecraft.SessionID, TriggerMessageID: msg.ID, Query: "查询当前时间", Tools: []tool.BaseTool{&reviewClock{count: &invoked}}, SystemInstruction: "使用 current_time 查询当前时间。", Done: func() { close(done) }, ToolEvent: func(event ToolEvent) { events = append(events, event) }})
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("tool test timeout")
	}
	if invoked.Load() != 1 || len(events) != 2 || events[0].Status != "started" || events[1].Status != "returned" {
		t.Fatalf("invoked=%d events=%+v", invoked.Load(), events)
	}
}

type reviewClock struct{ count *atomic.Int32 }

func (t *reviewClock) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "current_time", Desc: "获取当前时间", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}, nil
}
func (t *reviewClock) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	t.count.Add(1)
	return time.Now().UTC().Format(time.RFC3339), nil
}
