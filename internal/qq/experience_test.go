package qq

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mineagent/internal/agent"
	"mineagent/internal/config"
	"mineagent/internal/session"
	"mineagent/internal/storage"
)

func TestLocalMediaKindsPreserveBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind int
	}{{"动画.gif", FileImage}, {"sticker.webp", FileImage}, {"report.pdf", FileFile}, {"code.zip", FileFile}, {"clip.mp4", FileVideo}, {"voice.mp3", FileAudio}} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte("GIF89a-original-animation-or-file-bytes")
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.name), data, 0600); err != nil {
				t.Fatal(err)
			}
			var uploaded bytes.Buffer
			var base string
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if r.Method == http.MethodPut {
					b, _ := io.ReadAll(r.Body)
					uploaded.Write(b)
					fmt.Fprint(w, `{}`)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/upload_prepare"):
					if body["file_type"] != strconv.Itoa(tc.kind) || body["file_name"] != tc.name {
						t.Errorf("wrong prepare type/name: %v", body)
					}
					parts := []map[string]any{}
					for i := 0; i < (len(data)+7)/8; i++ {
						parts = append(parts, map[string]any{"index": i + 1, "presigned_url": base + "/part/" + strconv.Itoa(i+1)})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": "upload", "block_size": "8", "parts": parts})
				case strings.HasSuffix(r.URL.Path, "/upload_part_finish"):
					fmt.Fprint(w, `{}`)
				case strings.HasSuffix(r.URL.Path, "/files"):
					if body["file_type"] != float64(tc.kind) {
						t.Error("wrong merged type")
					}
					fmt.Fprint(w, `{"file_info":"confirmed-file","ttl":100}`)
				}
			})
			srv := httptest.NewServer(h)
			defer srv.Close()
			base = srv.URL
			api := NewAPI(base, &TokenSource{token: "fixture", expiresAt: time.Now().Add(time.Hour)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			got, err := api.UploadLocalMedia(context.Background(), "group", "G", root, tc.name, tc.kind)
			if err != nil || got != "confirmed-file" || !bytes.Equal(data, uploaded.Bytes()) {
				t.Fatalf("upload failed or changed bytes: info=%q err=%v", got, err)
			}
		})
	}
}
func TestLocalMediaRejectsEscapesAndOversize(t *testing.T) {
	api, _ := testAPI(t, okHandler(`{}`))
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.gif")
	_ = os.WriteFile(outside, []byte("secret"), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "escape.gif")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../secret.gif", outside, "escape.gif", "."} {
		if _, err := api.UploadLocalMedia(context.Background(), "c2c", "U", root, name, FileImage); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	f, err := os.Create(filepath.Join(root, "large.zip"))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Truncate((200 << 20) + 1)
	f.Close()
	if _, err := api.UploadLocalMedia(context.Background(), "c2c", "U", root, "large.zip", FileFile); err == nil {
		t.Error("accepted oversized file")
	}
}
func TestReplySequencesAndDefiniteFallbackOnly(t *testing.T) {
	api, reqs := testAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"id":"ok"}`) }))
	ctx := context.Background()
	_ = api.SendC2C(ctx, "U", "one", "M", 1)
	_ = api.SendC2CMarkdown(ctx, "U", "two", "M", 1)
	_ = api.SendC2CMedia(ctx, "U", "info", "M", 1)
	for i, m := range *reqs {
		if m["msg_seq"] != float64(i+1) {
			t.Fatalf("reused sequence: %v", *reqs)
		}
	}
	for _, code := range []int{40034005, 40034128, 40054005, 40034105, 50055002} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			n := 0
			a, _ := testAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n++
				if n == 1 {
					w.WriteHeader(400)
					fmt.Fprintf(w, `{"code":%d,"message":"secret-detail"}`, code)
				} else {
					fmt.Fprint(w, `{"id":"ok"}`)
				}
			}))
			err := a.SendC2C(ctx, "U", "hello", "M", 1)
			if code == 40034005 || code == 40034128 {
				if err != nil || n != 2 {
					t.Fatalf("definite expiry: err=%v n=%d", err, n)
				}
			} else {
				if err == nil || n != 1 || strings.Contains(err.Error(), "secret-detail") {
					t.Fatalf("unsafe fallback/error: err=%v n=%d", err, n)
				}
			}
		})
	}
}
func TestQQTextIsSplitWithoutLoss(t *testing.T) {
	api, requests := testAPI(t, okHandler(`{"id":"ok"}`))
	c := &Channel{api: api}
	text := strings.Repeat("中文🙂", 900)
	if err := c.sendText(context.Background(), "c2c:U:M", text); err != nil {
		t.Fatal(err)
	}
	var joined string
	for _, r := range *requests {
		joined += r["content"].(string)
	}
	if joined != text || len(*requests) != 3 {
		t.Fatal("long response was lost/truncated")
	}
}
func TestQQQueueHighFollowupAndCancellation(t *testing.T) {
	var mu sync.Mutex
	var prompts []string
	started := make(chan struct{})
	release := make(chan struct{})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Effort   string `json:"reasoning_effort"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Effort != "high" {
			t.Errorf("reasoning=%q", body.Effort)
		}
		mu.Lock()
		n := len(prompts)
		encoded, _ := json.Marshal(body.Messages)
		prompts = append(prompts, string(encoded))
		mu.Unlock()
		if n == 0 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		fmt.Fprint(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"previous-result"},"finish_reason":"stop"}]}`)
	}))
	defer model.Close()
	st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := config.Default()
	cfg.Model.BaseURL = model.URL
	cfg.Model.Name = "fixture"
	cfg.QQ.StreamProgress = false
	cfg.Agent.MaxConcurrentRuns = 1
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
	channel.onMessage(InboundMessage{Kind: "c2c", MsgID: "M1", UserOpenID: "U", Text: "first"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first did not start")
	}
	// No debounce loss: these arrive immediately while the first task is blocked.
	channel.onMessage(InboundMessage{Kind: "c2c", MsgID: "M2", UserOpenID: "U", Text: "second"})
	channel.onMessage(InboundMessage{Kind: "c2c", MsgID: "M3", UserOpenID: "U", Text: "cancel-third"})
	channel.mu.Lock()
	tasks := append([]*qqTask(nil), channel.tasks["qq:c2c:U"]...)
	channel.mu.Unlock()
	if len(tasks) != 3 {
		t.Fatalf("rapid requests dropped: %d", len(tasks))
	}
	tasks[2].cancel()
	close(release)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		channel.mu.Lock()
		n := len(channel.tasks["qq:c2c:U"])
		channel.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 2 {
		t.Fatalf("cancelled queued model call ran / queue stuck: calls=%d", len(prompts))
	}
	if !strings.Contains(prompts[1], "previous-result") || !strings.Contains(prompts[1], "second") || strings.Contains(prompts[1], "cancel-third") {
		t.Fatalf("incorrect queued context: %s", prompts[1])
	}
}
func TestGroupCancellationIsScopedToCaller(t *testing.T) {
	c := &Channel{tasks: map[string][]*qqTask{}}
	a := c.newTask("group", "A", "group:G:M")
	b := c.newTask("group", "B", "group:G:N")
	c.taskCommand("group", "A", false, "stop")
	if a.ctx.Err() == nil || b.ctx.Err() != nil {
		t.Fatal("stopped another group member's task")
	}
	c.taskCommand("group", "admin", true, "stop")
	if b.ctx.Err() == nil {
		t.Fatal("admin could not stop")
	}
}

func TestStreamProtocolKeepsIDSequencePrefixAndDone(t *testing.T) {
	api, reqs := testAPI(t, okHandler(`{"id":"stream-1"}`))
	ctx := context.Background()
	seq := api.nextSequence("/v2/users/U/messages", "M")
	id, err := api.sendStream(ctx, "U", "M", "", "· 开始", 0, seq, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.sendStream(ctx, "U", "M", id, "· 开始\n· 完成", 1, seq, true)
	if err != nil {
		t.Fatal(err)
	}
	if (*reqs)[0]["stream_msg_id"] != "" || (*reqs)[1]["stream_msg_id"] != "stream-1" || (*reqs)[1]["input_state"] != float64(10) || (*reqs)[1]["index"] != float64(1) {
		t.Fatalf("bad stream frames: %v", *reqs)
	}
	if (*reqs)[0]["msg_seq"] != (*reqs)[1]["msg_seq"] {
		t.Fatal("stream frame sequence changed")
	}
	_ = api.SendC2C(ctx, "U", "final", "M", 0)
	if (*reqs)[2]["msg_seq"] != float64(2) {
		t.Fatal("stream did not reserve shared reply sequence")
	}
}
func TestMediaBusinessErrorOnHTTP200IsNotSuccess(t *testing.T) {
	api, _ := testAPI(t, okHandler(`{"err_code":40034004,"message":"secret-detail","file_info":"misleading"}`))
	if _, err := api.UploadC2CImageURL(context.Background(), "U", "https://example.com/a.png"); err == nil || strings.Contains(err.Error(), "secret-detail") {
		t.Fatalf("business error ignored or leaked: %v", err)
	}
}
