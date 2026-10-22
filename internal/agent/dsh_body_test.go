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

	"mineagent/internal/body"
	"mineagent/internal/config"
	"mineagent/internal/dshbridge"
	"mineagent/internal/session"
	"mineagent/internal/storage"
)

// Exercise Submit -> DSH transport -> actual body tools. No model, game,
// production identity or credentials are involved.
func TestDSHBodyMergePreservesBudgetsAndPublicScope(t *testing.T) {
	for _, provider := range []string{"deepseek-official", "opencode-go"} {
		for _, mode := range []string{"body-default", "body-unlimited", "body-high", "body-conversation-high", "public-mc"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				isBody := mode != "public-mc"
				type result struct {
					Value string `json:"value"`
					Error bool   `json:"error"`
				}
				results := make(chan result, 1)
				bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.Contains(r.URL.Path, "/tools/") {
						var v result
						if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
							t.Error(err)
						}
						results <- v
						fmt.Fprint(w, `{}`)
						return
					}
					if r.URL.Path != "/v1/runs" {
						fmt.Fprint(w, `{"stopped":true}`)
						return
					}
					var req dshbridge.Request
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					expectedSteps, expectedEffort := 12, "low"
					if mode == "body-unlimited" {
						expectedSteps = 0
					} else if mode == "body-high" || mode == "body-conversation-high" {
						expectedEffort = "high"
					} else if !isBody {
						expectedSteps, expectedEffort = 8, "high"
					}
					if req.MaxIterations != expectedSteps || req.ReasoningEffort != expectedEffort {
						t.Errorf("lost budget: iterations=%d effort=%s", req.MaxIterations, req.ReasoningEffort)
					}
					if !isBody && (len(req.Tools) != 1 || req.Tools[0].Name != "body_profile") {
						t.Error("public MC received full body tools")
					}
					w.Header().Set("Content-Type", "application/x-ndjson")
					calls := []string{"body_status", "body_status", "body_status", "body_status"}
					if !isBody {
						calls = []string{"body_status", "body_profile"}
					}
					for i, name := range calls {
						fmt.Fprintf(w, `{"type":"tool","id":"read-%d","name":%q,"arguments":{}}`+"\n", i, name)
						w.(http.Flusher).Flush()
						var got result
						select {
						case got = <-results:
						case <-r.Context().Done():
							return
						}
						var data map[string]any
						_ = json.Unmarshal([]byte(got.Value), &data)
						if isBody {
							if got.Error || (i < 3 && data["ok"] != true) || (i == 3 && data["code"] != "observation_budget") {
								t.Errorf("body read %d bypassed budget or lost identity: %+v", i, got)
							}
						} else if i == 0 {
							if !got.Error {
								t.Error("unregistered body_status was executed")
							}
						} else if got.Error || data["name"] == nil || data["connectionId"] != nil || data["state"] != nil || data["position"] != nil {
							t.Errorf("public projection lost privacy: %+v", got)
						}
					}
					fmt.Fprintln(w, `{"type":"done","text":"body-merge-verified"}`)
				}))
				defer bridge.Close()
				cfg := config.Default()
				cfg.Model.Name, cfg.Model.BaseURL, cfg.Model.APIKey = "fixture", "http://127.0.0.1:1", "fixture"
				cfg.Body.Enabled, cfg.Body.Mode = true, "assist"
				if mode == "body-unlimited" {
					cfg.Body.MaxSteps = 0
				} else if mode == "body-high" {
					cfg.Body.ReasoningEffort = "high"
				}
				tokenFile := filepath.Join(t.TempDir(), "bridge.token")
				if err := os.WriteFile(tokenFile, []byte(strings.Repeat("a", 64)), 0600); err != nil {
					t.Fatal(err)
				}
				cfg.Agent.Backend = "dsh"
				cfg.Agent.DSH = dshbridge.Config{URL: bridge.URL, TokenFile: tokenFile, Route: dshbridge.Route{Provider: provider, Model: "deepseek-v4.1-flash"}}
				st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				log := slog.New(slog.NewTextHandler(io.Discard, nil))
				core := body.New(cfg.Body, st, nil, log)
				core.WithAuthorization(func(context.Context, int64, int64) bool { return true })
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				a, err := New(ctx, cfg, st, log, core.PublicTools(), nil)
				if err != nil {
					t.Fatal(err)
				}
				go a.Run(ctx)
				sess, err := session.NewHub(st, log).Session(ctx, "minecraft-main")
				if err != nil {
					t.Fatal(err)
				}
				channel := &fakeChannel{}
				sess.Register(channel)
				msg, err := sess.Ingest(ctx, storage.Message{Channel: "minecraft", AuthorKind: "player", Text: "verify body merge"})
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				req := Request{Session: sess, SessionKey: "minecraft-main", Query: msg.Text, TriggerMessageID: msg.ID, ReasoningEffort: "medium", Done: func() { close(done) }}
				if isBody {
					req.BodyPrincipal, req.Tools = core.Principal(1, 1, "owner"), core.Tools()
					req.ReasoningEffort = "high" // Body configuration overrides general chat preferences.
					if mode == "body-conversation-high" {
						req.ConversationEffort = true
					}
				}
				if !a.Submit(req) {
					t.Fatal("submit failed")
				}
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal("DSH body merge test timed out")
				}
				if last, ok := channel.last(); !ok || last.Text != "body-merge-verified" {
					t.Fatalf("unexpected reply: %+v", last)
				}
			})
		}

	}
}
