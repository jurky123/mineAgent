package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"mineagent/internal/config"
	"mineagent/internal/dshbridge"
	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

type bridgeApprovalTool struct {
	ap       *tools.Approvals
	executed atomic.Int32
}

func (t *bridgeApprovalTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "fixture", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{})}, nil
}
func (t *bridgeApprovalTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	if tools.RequesterFromContext(ctx) != "qq:owner" || tools.ReplyTargetFromContext(ctx) != "c2c:owner:message" {
		return "", fmt.Errorf("identity lost")
	}
	if resumed, _, d := tools.ApprovalResumeContext(ctx); resumed {
		if d.Approved && ctx.Err() == nil {
			t.executed.Add(1)
			return "executed", nil
		}
		return "denied", nil
	}
	info, err := t.ap.CreateForContext(ctx, tools.ApprovalInfo{Source: "body", Tool: "fixture", Requester: "qq:owner"})
	if err != nil {
		return "", err
	}
	return "", tools.InterruptForApproval(ctx, &info)
}
func TestDSHApprovalDirectDeliveryPreservesIdentityAndCancellation(t *testing.T) {
	for _, mode := range []string{"approve", "deny", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ap := tools.NewApprovals(nil, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
			tl := &bridgeApprovalTool{ap: ap}
			a := &Agent{approvals: ap}
			ctx, cancel := context.WithCancel(tools.WithReplyTarget(tools.WithRequester(context.Background(), "qq:owner"), "c2c:owner:message"))
			defer cancel()
			out, err := a.invokeDSHTool(ctx, Request{}, tl, "{}", func() {
				infos := ap.BodyPending()
				if len(infos) != 1 {
					t.Fatal("missing pending approval")
				}
				if mode == "cancel" {
					cancel()
				} else if !ap.DecideBody(infos[0].ApprovalID, mode == "approve", "admin") {
					t.Fatal("decision rejected")
				}
			})
			if mode == "approve" {
				if out != "executed" || err != nil || tl.executed.Load() != 1 {
					t.Fatalf("out=%s err=%v count=%d", out, err, tl.executed.Load())
				}
			} else if tl.executed.Load() != 0 {
				t.Fatal("unapproved operation ran")
			}
			if mode == "cancel" && err == nil {
				t.Fatal("cancel not propagated")
			}
			if ap.Pending() != 0 || len(ap.DrainDecided()) != 0 {
				t.Fatal("approval leaked into Eino resume queue")
			}
		})
	}
}
func TestDSHRoutingQueueScopedInputsAndPrompt(t *testing.T) {
	var invoked atomic.Int32
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs" {
			fmt.Fprint(w, `{"stopped":true}`)
			return
		}
		var input dshbridge.Request
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		if input.Instruction == "" || !strings.Contains(input.Instruction, "custom-personality") || !strings.Contains(input.Instruction, "只使用本次列出的工具") || input.ReasoningEffort != "high" {
			t.Error("lost prompt or effort")
		}
		if len(input.Tools) != 1 || input.Tools[0].Name != "fixture" {
			t.Error("unexpected tool set")
		}
		found := false
		for _, m := range input.Messages {
			for _, b := range m.Content {
				if b.Text == "prepared-input" {
					found = true
				}
			}
		}
		if !found {
			t.Error("prepared input missing")
		}
		invoked.Add(1)
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"type":"done","text":"bridge-result"}`)
	}))
	defer bridge.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	token := filepath.Join(t.TempDir(), "token")
	os.WriteFile(token, []byte(strings.Repeat("a", 64)), 0600)
	cfg := config.Default()
	cfg.Model.BaseURL = "http://127.0.0.1:1/v1"
	cfg.Model.APIKey = "fixture"
	cfg.Model.Name = "primary"
	cfg.Agent.Backend = "dsh"
	cfg.Agent.DSH = dshbridge.Config{URL: bridge.URL, TokenFile: token, Route: dshbridge.Route{Provider: "fixture", Model: "routed"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := New(ctx, cfg, store, log, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	go a.Run(ctx)
	hub := session.NewHub(store, log)
	sess, err := hub.Session(ctx, "qq:c2c:owner")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := sess.Ingest(ctx, storage.Message{Channel: "qq", AuthorKind: "player", Text: "question", Target: "c2c:owner:message"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	req := Request{Context: ctx, Done: func() { close(done) }, Session: sess, SessionKey: "qq:c2c:owner", Player: "qq:owner", ReplyTarget: "c2c:owner:message", TriggerMessageID: msg.ID, SystemInstruction: "custom-personality", Tools: []tool.BaseTool{&bridgeApprovalTool{}}, Prepare: func(ctx context.Context) (context.Context, *schema.Message, error) {
		return ctx, schema.UserMessage("prepared-input"), nil
	}}
	if !a.Submit(req) {
		t.Fatal("request refused")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("queue stalled")
	}
	rows, err := store.RecentMessages(ctx, req.SessionKey, 10)
	if err != nil {
		t.Fatal(err)
	}
	if invoked.Load() != 1 || rows[len(rows)-1].Text != "bridge-result" {
		t.Fatal("bridge reply missing")
	}
}
