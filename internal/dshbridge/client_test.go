package dshbridge

import (
	"context"
	"encoding/json"
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
