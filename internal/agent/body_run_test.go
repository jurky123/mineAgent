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
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"mineagent/internal/body"
	"mineagent/internal/config"
	"mineagent/internal/session"
	"mineagent/internal/storage"
)

// Exercise the real Eino runner beyond all former 12/32/80 round ceilings and
// beyond the outer conversation timeout, with actual tool invocations.
func TestBodyTaskContinuesUntilModelFinishes(t *testing.T)    { testContinuousTask(t, true, 98, true) }
func TestQQTaskContinuesBeyondFormerLimits(t *testing.T)      { testContinuousTask(t, false, 0, true) }
func TestDefaultBodyBudgetStopsRepeatedToolLoop(t *testing.T) { testContinuousTask(t, true, 0, false) }
func testContinuousTask(t *testing.T, isBody bool, maxIterations int, expectComplete bool) {
	t.Helper()
	const rounds = 96
	var modelCalls, invoked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		n := modelCalls.Add(1)
		time.Sleep(15 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		if n <= int32(rounds) {
			fmt.Fprintf(w, `{"id":"%d","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"step-%d","type":"function","function":{"name":"current_time","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, n, n)
		} else {
			fmt.Fprint(w, `{"id":"done","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"任务已核验完成"},"finish_reason":"stop"}]}`)
		}
	}))
	defer srv.Close()
	st, err := storage.Open(filepath.Join(t.TempDir(), "run.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	cfg.Model.BaseURL, cfg.Model.APIKey, cfg.Model.Name = srv.URL, "fixture", "test-model"
	cfg.Agent.RunTimeoutSec = 1
	if !isBody {
		cfg.Agent.RunTimeoutSec = 0
	}
	cfg.Agent.SummaryMessages, cfg.Agent.SummaryTokens = 100000, 1000000
	cfg.Agent.ReductionTokens = 2000000
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	hub := session.NewHub(st, log)
	sess, err := hub.Session(ctx, "web:body-long-task")
	if err != nil {
		t.Fatal(err)
	}
	ch := &fakeChannel{}
	sess.Register(ch)
	msg, err := sess.Ingest(ctx, storage.Message{Channel: "web", AuthorKind: "player", Text: "持续推进直到完成"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(ctx, cfg, st, log, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	go a.Run(ctx)
	done := make(chan struct{})
	start := time.Now()
	request := Request{MaxIterations: maxIterations, Session: sess, SessionKey: "web:body-long-task", Query: msg.Text, TriggerMessageID: msg.ID,
		Tools: []tool.BaseTool{&reviewClock{count: &invoked}}, SystemInstruction: "执行全部步骤后核验完成", Done: func() { close(done) }}
	if isBody {
		request.BodyPrincipal = &body.Principal{UserID: 1}
	} else {
		request.ReasoningEffort = "high"
	}
	if !a.Submit(request) {
		t.Fatal("submit")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("task did not finish")
	}
	last, ok := ch.last()
	if expectComplete {
		if !ok || !strings.Contains(last.Text, "任务已核验完成") || invoked.Load() != int32(rounds) || time.Since(start) < time.Second {
			t.Fatalf("explicit task budget failed: tools=%d reply=%q elapsed=%s", invoked.Load(), last.Text, time.Since(start))
		}
	} else if invoked.Load() < 1 || invoked.Load() > 12 || modelCalls.Load() > 12 || strings.Contains(last.Text, "任务已核验完成") {
		t.Fatalf("default body loop was unbounded: tools=%d model=%d reply=%q", invoked.Load(), modelCalls.Load(), last.Text)
	}
}

func TestBodyDeadlineAndChatDeadlineRemainIndependent(t *testing.T) {
	a := &Agent{cfg: config.Agent{RunTimeoutSec: 1}}
	ctx, cancel := a.requestContext(context.Background(), Request{BodyPrincipal: &body.Principal{}})
	defer cancel()
	if _, limited := ctx.Deadline(); limited {
		t.Fatal("unlimited body inherited chat deadline")
	}
	deadline := time.Now().Add(time.Hour)
	ctx2, cancel2 := a.requestContext(context.Background(), Request{BodyPrincipal: &body.Principal{Deadline: deadline}})
	defer cancel2()
	if got, ok := ctx2.Deadline(); !ok || !got.Equal(deadline) {
		t.Fatal("configured body deadline lost", got)
	}
	ctx3, cancel3 := a.requestContext(context.Background(), Request{})
	defer cancel3()
	if got, ok := ctx3.Deadline(); !ok || time.Until(got) > time.Second {
		t.Fatal("chat timeout changed", got)
	}
}
