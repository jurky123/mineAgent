package dshbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func clientFixture(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	token := filepath.Join(t.TempDir(), "bridge.token")
	if err := os.WriteFile(token, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{URL: server.URL, TokenFile: token, Route: Route{Provider: "fixture", Model: "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestRejectUntrustedOriginsAndTokens(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	os.WriteFile(file, []byte(strings.Repeat("a", 64)), 0600)
	for _, url := range []string{"https://127.0.0.1:1234", "http://localhost:1234", "http://192.0.2.1:1234", "http://127.0.0.1", "http://user@127.0.0.1:1234", "http://127.0.0.1:1234/path", "http://127.0.0.1:1234?token=x"} {
		if _, err := New(Config{URL: url, TokenFile: file, Route: Route{Provider: "p", Model: "m"}}); err == nil {
			t.Fatalf("accepted %s", url)
		}
	}
	os.Chmod(file, 0644)
	if _, err := New(Config{URL: "http://127.0.0.1:1234", TokenFile: file, Route: Route{Provider: "p", Model: "m"}}); err == nil {
		t.Fatal("accepted public token file")
	}
}
func TestDuplicateToolCallNeverExecutesTwice(t *testing.T) {
	var executed, cancelled atomic.Int32
	resultReceived := make(chan struct{})
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("missing authentication")
		}
		switch {
		case r.URL.Path == "/v1/runs":
			w.Header().Set("Content-Type", "application/x-ndjson")
			fmt.Fprintln(w, `{"type":"tool","id":"one","name":"fixture","arguments":{}}`)
			w.(http.Flusher).Flush()
			<-resultReceived
			fmt.Fprintln(w, `{"type":"tool","id":"one","name":"fixture","arguments":{}}`)
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			cancelled.Add(1)
			fmt.Fprint(w, `{"stopped":true}`)
		default:
			close(resultReceived)
			fmt.Fprint(w, `{"stopped":true}`)
		}
	})
	_, err := c.Run(context.Background(), Request{}, func(context.Context, string, string) (string, error) { executed.Add(1); return "done", nil })
	if err == nil || executed.Load() != 1 || cancelled.Load() != 1 {
		t.Fatalf("err=%v executed=%d cancelled=%d", err, executed.Load(), cancelled.Load())
	}
}
func TestUnconfirmedCancellationBlocksLaterRuns(t *testing.T) {
	var accepted atomic.Int32
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs" {
			accepted.Add(1)
			w.Header().Set("Content-Type", "application/x-ndjson")
			fmt.Fprintln(w, `{"type":"ready"}`)
			return
		}
		w.WriteHeader(503)
	})
	_, err := c.Run(context.Background(), Request{}, nil)
	if err == nil {
		t.Fatal("accepted incomplete stream")
	}
	_, err = c.Run(context.Background(), Request{}, nil)
	if err == nil || accepted.Load() != 1 {
		t.Fatalf("uncertain request replayed: %v %d", err, accepted.Load())
	}
}
func TestSerialRunsAndCancellationWhileWaiting(t *testing.T) {
	first := make(chan struct{})
	release := make(chan struct{})
	var accepted atomic.Int32
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs" {
			fmt.Fprint(w, `{"stopped":true}`)
			return
		}
		accepted.Add(1)
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"type":"ready"}`)
		w.(http.Flusher).Flush()
		close(first)
		<-release
		fmt.Fprintln(w, `{"type":"done","text":"OK"}`)
	})
	done := make(chan error, 1)
	go func() { _, err := c.Run(context.Background(), Request{}, nil); done <- err }()
	<-first
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Run(ctx, Request{}, nil)
	if err == nil || accepted.Load() != 1 {
		t.Fatal("cancelled waiting task reached backend")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestToolResultAndNoRedirect(t *testing.T) {
	var received atomic.Bool
	resultReceived := make(chan struct{})
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs" {
			w.Header().Set("Content-Type", "application/x-ndjson")
			fmt.Fprintln(w, `{"type":"tool","id":"one","name":"fixture","arguments":{"x":1}}`)
			w.(http.Flusher).Flush()
			<-resultReceived
			fmt.Fprintln(w, `{"type":"done","text":"OK"}`)
			return
		}
		var result struct {
			Value string `json:"value"`
			Error bool   `json:"error"`
		}
		if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
			t.Error(err)
		}
		received.Store(result.Value == "safe" && !result.Error)
		close(resultReceived)
		io.WriteString(w, `{}`)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := c.Run(ctx, Request{}, func(_ context.Context, name, args string) (string, error) {
		if name != "fixture" || args != `{"x":1}` {
			t.Error("tool arguments changed")
		}
		return "safe", nil
	})
	if err != nil || out != "OK" || !received.Load() {
		t.Fatalf("out=%s err=%v result=%v", out, err, received.Load())
	}
	c.cfg.Models = map[string]Route{"router/x": {Provider: "p", Model: "x"}}
	if r, ok := c.Route("router/x", "primary"); !ok || r.Model != "x" {
		t.Fatal("route map")
	}
	if _, ok := c.Route("other", "primary"); ok {
		t.Fatal("unexpected route")
	}
}

func TestDSHDisconnectCancelsAndDrainsLocalTool(t *testing.T) {
	for _, terminal := range []string{"disconnect", "cancel"} {
		t.Run(terminal, func(t *testing.T) {
			started := make(chan struct{})
			stopped := make(chan struct{})
			c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/runs" {
					fmt.Fprint(w, `{"stopped":true}`)
					return
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				fmt.Fprintln(w, `{"type":"tool","id":"one","name":"fixture","arguments":{}}`)
				w.(http.Flusher).Flush()
				<-started
				if terminal == "cancel" {
					fmt.Fprintln(w, `{"type":"error","code":"UNKNOWN"}`)
				}
			})
			done := make(chan error, 1)
			go func() {
				_, err := c.Run(context.Background(), Request{}, func(ctx context.Context, _, _ string) (string, error) {
					close(started)
					<-ctx.Done()
					close(stopped)
					return "", ctx.Err()
				})
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("unexpected success")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("DSH disconnect left local tool running")
			}
			select {
			case <-stopped:
			default:
				t.Fatal("client returned before local tool quiesced")
			}
		})
	}
}

func TestPublicRunErrorIsTypedSanitizedAndNeverReplayed(t *testing.T) {
	for _, code := range []string{"UNSUPPORTED_REASONING_EFFORT", "untrusted-provider-detail"} {
		t.Run(code, func(t *testing.T) {
			var requests atomic.Int32
			c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/x-ndjson")
				frame, _ := json.Marshal(map[string]string{"type": "error", "code": code})
				fmt.Fprintln(w, string(frame))
			})
			_, err := c.Run(context.Background(), Request{}, nil)
			var runError *RunError
			if !errors.As(err, &runError) {
				t.Fatalf("missing typed error: %v", err)
			}
			want := code
			if code == "untrusted-provider-detail" {
				want = "UNKNOWN"
			}
			if runError.Code != want || requests.Load() != 1 || strings.Contains(err.Error(), "untrusted-provider-detail") {
				t.Fatalf("err=%v requests=%d", err, requests.Load())
			}
		})
	}
}

func TestRecoveryRequiresExactOldRunStopAndNeverReplays(t *testing.T) {
	var stopOK atomic.Bool
	var starts, tools, cancels atomic.Int32
	var firstID atomic.Value
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs" {
			var req Request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			n := starts.Add(1)
			w.Header().Set("Content-Type", "application/x-ndjson")
			if n == 1 {
				firstID.Store(req.ID)
				fmt.Fprintln(w, `{"type":"ready"}`)
				return
			}
			if req.Model != "new-model" {
				t.Error("new selection not applied")
			}
			if req.ID == firstID.Load().(string) {
				t.Error("old request replayed")
			}
			fmt.Fprintln(w, `{"type":"tool","id":"fresh-tool","name":"fixture","arguments":{}}`)
			w.(http.Flusher).Flush()
			for tools.Load() == 0 {
				time.Sleep(time.Millisecond)
			}
			fmt.Fprintln(w, `{"type":"done","text":"new task done"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			cancels.Add(1)
			if r.URL.Path != "/v1/runs/"+firstID.Load().(string)+"/cancel" {
				t.Error("wrong recovery ID")
			}
			fmt.Fprintf(w, `{"stopped":%t}`, stopOK.Load())
			return
		}
		tools.Add(1)
		fmt.Fprint(w, `{}`)
	})
	if _, err := c.Run(context.Background(), Request{Model: "old-model"}, nil); err == nil {
		t.Fatal("incomplete run accepted")
	}
	if _, err := c.Run(context.Background(), Request{Model: "new-model"}, nil); !errors.Is(err, ErrStopUnconfirmed) {
		t.Fatal("unconfirmed recovery allowed", err)
	}
	if starts.Load() != 1 || cancels.Load() != 2 {
		t.Fatal("request replayed before stop ack")
	}
	stopOK.Store(true)
	text, err := c.Run(context.Background(), Request{Model: "new-model"}, func(context.Context, string, string) (string, error) { return "result", nil })
	if err != nil || text != "new task done" || starts.Load() != 2 || tools.Load() != 1 || cancels.Load() != 3 {
		t.Fatal(text, err, starts.Load(), tools.Load(), cancels.Load())
	}
	if c.fault {
		t.Fatal("confirmed stop did not release fault")
	}
}

func TestUnavailableHostRecoversAfterAuthenticatedCancellation(t *testing.T) {
	var online atomic.Bool
	var starts, cancels atomic.Int32
	c := clientFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if !online.Load() {
			w.WriteHeader(503)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			cancels.Add(1)
			fmt.Fprint(w, `{"stopped":true}`)
			return
		}
		starts.Add(1)
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"type":"done","text":"recovered"}`)
	})
	if _, err := c.Run(context.Background(), Request{}, nil); err == nil {
		t.Fatal("offline request accepted")
	}
	if _, err := c.Run(context.Background(), Request{}, nil); !errors.Is(err, ErrStopUnconfirmed) {
		t.Fatal(err)
	}
	online.Store(true)
	if text, err := c.Run(context.Background(), Request{}, nil); err != nil || text != "recovered" {
		t.Fatal(text, err)
	}
	if starts.Load() != 1 || cancels.Load() != 1 {
		t.Fatal("unexpected replay", starts.Load(), cancels.Load())
	}
}
