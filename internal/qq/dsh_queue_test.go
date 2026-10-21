package qq

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
	"sync"
	"testing"
	"time"

	"mineagent/internal/agent"
	"mineagent/internal/config"
	"mineagent/internal/dshbridge"
	"mineagent/internal/session"
	"mineagent/internal/storage"
)

func TestDSHQueuedQQContextEndsAtTrigger(t *testing.T) {
	for _, scenario := range []string{"same-session", "different-session", "new-conversation", "group-same-session", "group-different-session", "group-new-conversation"} {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			var requests []dshbridge.Request
			started, release := make(chan struct{}), make(chan struct{})
			bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/runs" {
					fmt.Fprint(w, `{"stopped":true}`)
					return
				}
				var input dshbridge.Request
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				mu.Lock()
				n := len(requests)
				requests = append(requests, input)
				mu.Unlock()
				if n == 0 {
					close(started)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				// This is the actual bridge admission contract, missed by the old Eino-only test.
				if len(input.Messages) == 0 || input.Messages[len(input.Messages)-1].Role != "user" {
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				fmt.Fprintln(w, `{"type":"done","text":"previous-result"}`)
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
			cfg.Model.BaseURL = "http://127.0.0.1:1"
			cfg.Agent.Backend = "dsh"
			cfg.Agent.MaxConcurrentRuns = 1
			cfg.QQ.StreamProgress = false
			cfg.Agent.DSH = dshbridge.Config{URL: bridge.URL, TokenFile: token, Route: dshbridge.Route{Provider: "fixture", Model: "fixture"}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ag, err := agent.New(ctx, cfg, st, log, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			go ag.Run(ctx)
			api, _ := testAPI(t, okHandler(`{"id":"ok"}`))
			channel := NewChannel(log, cfg, session.NewHub(st, log), st, ag, api, nil)
			scene, kind := "c2c", "c2c"
			if strings.HasPrefix(scenario, "group-") {
				scene, kind = "group", "group_at"
			}
			send := func(id, user, text string) {
				channel.onMessage(InboundMessage{Kind: kind, MsgID: id, UserOpenID: user, GroupOpenID: user, Text: text})
			}
			send("M1", "U", "first question")
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("first task did not start")
			}
			user := "U"
			if strings.HasSuffix(scenario, "different-session") {
				user = "V"
			}
			if strings.HasSuffix(scenario, "new-conversation") {
				send("NEW", "U", "/new")
			}
			send("M2", user, "second question")
			// A second conversation must remain queued under the configured global FIFO.
			mu.Lock()
			count := len(requests)
			mu.Unlock()
			if count != 1 {
				t.Fatal("global queue was bypassed")
			}
			close(release)
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				channel.mu.Lock()
				count = 0
				for _, tasks := range channel.tasks {
					count += len(tasks)
				}
				channel.mu.Unlock()
				if count == 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if count != 0 {
				t.Fatal("queue did not drain")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 2 {
				t.Fatalf("backend calls=%d, want 2", len(requests))
			}
			second := requests[1]
			last := second.Messages[len(second.Messages)-1]
			if last.Role != "user" {
				t.Fatalf("queued request ends with %s instead of the user trigger", last.Role)
			}
			encoded, _ := json.Marshal(second.Messages)
			text := string(encoded)
			if !strings.Contains(text, "second question") || strings.Contains(text, "已加入任务队列") {
				t.Fatalf("queue notice contaminated model history: %s", text)
			}
			hasPrevious := strings.Contains(text, "previous-result")
			if hasPrevious != (strings.HasSuffix(scenario, "same-session")) {
				t.Fatalf("late reply lost or leaked between conversations: %s", text)
			}
			active, err := st.ActiveQQConversation(ctx, "qq:"+scene+":"+user, time.Now().UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			messages, err := st.RecentMessages(ctx, active.SessionID, 20)
			if err != nil {
				t.Fatal(err)
			}
			foundNotice, foundReply := false, false
			for _, m := range messages {
				if strings.Contains(m.Text, "已加入任务队列") {
					foundNotice = true
					if m.AuthorKind != "system" {
						t.Fatal("queue acknowledgement stored as assistant output")
					}
				}
				if m.Target == scene+":"+user+":M2" && m.AuthorKind == "agent" && m.Text == "previous-result" {
					foundReply = true
				}
			}
			if !foundNotice || !foundReply {
				t.Fatal("queued notice or successful final reply missing")
			}
		})
	}
}
