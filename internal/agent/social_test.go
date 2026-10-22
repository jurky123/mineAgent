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
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"mineagent/internal/config"
	"mineagent/internal/dshbridge"
	"mineagent/internal/session"
	"mineagent/internal/storage"
)

func TestDSHSocialHasNoToolsAndMayStaySilent(t *testing.T) {
	for _, reply := range []string{"这天气适合窝着。", "[[QQ_SILENT]]"} {
		t.Run(reply, func(t *testing.T) {
			bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input dshbridge.Request
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if len(input.Tools) != 0 || input.MaxIterations != 1 || input.ReasoningEffort != "high" || !strings.Contains(input.Instruction, "custom-whale-persona") || strings.Contains(input.Instruction, "可用共享技能") {
					t.Error("optional request inherited execution capabilities or lost persona/effort")
				}
				if len(input.Messages) == 0 || input.Messages[len(input.Messages)-1].Role != "user" {
					t.Error("not anchored to user input")
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				_ = json.NewEncoder(w).Encode(map[string]string{"type": "done", "text": reply})
			}))
			defer bridge.Close()
			st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			token := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(token, []byte(strings.Repeat("a", 64)), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			cfg.Model.APIKey = "fixture"
			cfg.Model.Name = "primary"
			cfg.Agent.Backend = "dsh"
			cfg.Model.BaseURL = "http://127.0.0.1:1/v1"
			cfg.Agent.DSH = dshbridge.Config{URL: bridge.URL, TokenFile: token, Route: dshbridge.Route{Provider: "fixture", Model: "model"}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			a, err := New(ctx, cfg, st, log, []tool.BaseTool{&bridgeApprovalTool{}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			go a.Run(ctx)
			sess, err := session.NewHub(st, log).Session(ctx, "qq:group:G")
			if err != nil {
				t.Fatal(err)
			}
			msg, err := sess.Ingest(ctx, storage.Message{Channel: "qq", AuthorKind: "player", AuthorID: "qq:member:G:A", Text: "下雨了", Target: "group:G:M"})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			if !a.Submit(Request{Social: true, Context: ctx, Session: sess, SessionKey: sess.ID, ReplyTarget: "group:G:M", TriggerMessageID: msg.ID, ReasoningEffort: "high", SystemInstruction: "custom-whale-persona", Done: func() { close(done) }}) {
				t.Fatal("refused")
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("social bridge stalled")
			}
			rows, err := st.RecentMessages(ctx, sess.ID, 10)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if reply != "[[QQ_SILENT]]" {
				want = 2
			}
			if len(rows) != want {
				t.Fatal(fmt.Sprintf("rows=%d want %d", len(rows), want))
			}
		})
	}
}
