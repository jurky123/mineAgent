package dshbridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The launcher starts an isolated installed DSH Web host and a fake model.
func TestInstalledDSH(t *testing.T) {
	endpoint := os.Getenv("MINEAGENT_TEST_DSH_URL")
	if endpoint == "" {
		t.Skip("run scripts/dsh-bridge-e2e.py")
	}
	c, err := New(Config{URL: endpoint, TokenFile: os.Getenv("MINEAGENT_TEST_DSH_TOKEN_FILE"), Route: Route{Provider: "deepseek-official", Model: "deepseek-flash"}})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	request := func(query string) Request {
		return Request{Route: c.cfg.Route, ReasoningEffort: "high", Instruction: "Answer the current task. Only use the listed tools.", Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: "Earlier user question"}}}, {Role: "assistant", Content: []Block{{Type: "text", Text: "Earlier assistant reply"}}}, {Role: "user", Content: []Block{{Type: "text", Text: "Second user question"}}}, {Role: "assistant", Content: []Block{{Type: "text", Text: "Second assistant reply"}}}, {Role: "user", Content: []Block{{Type: "text", Text: query}}}}, Tools: []Tool{{Name: "fixture_tool", Description: "A harmless test tool", Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`)}}}
	}
	invoke := func(ctx context.Context, name, args string) (string, error) {
		if name != "fixture_tool" || !strings.Contains(args, "fixture") {
			t.Errorf("unexpected proxy: %s %s", name, args)
		}
		calls.Add(1)
		return "fixture-result", nil
	}
	for _, q := range []string{"Reply OK", "TOOLTEST", "NATIVETEST"} {
		t.Run(q, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			out, err := c.Run(ctx, request(q), invoke)
			if err != nil || out != "OK" {
				t.Fatalf("out=%q err=%v", out, err)
			}
		})
	}
	if calls.Load() != 1 {
		t.Fatalf("native or duplicate tool executed: %d", calls.Load())
	}
	t.Run("image", func(t *testing.T) {
		r := request("Describe image")
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		img.Set(0, 0, color.RGBA{R: 255, A: 255})
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		r.Messages[len(r.Messages)-1].Content = append(r.Messages[len(r.Messages)-1].Content, Block{Type: "image", MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(buf.Bytes())})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := c.Run(ctx, r, invoke); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, err := c.Run(ctx, request("CANCELTEST"), func(ctx context.Context, _, _ string) (string, error) { cancel(); <-ctx.Done(); return "", ctx.Err() })
		if err == nil {
			t.Fatal("cancel succeeded")
		}
		if c.fault {
			t.Fatal("DSH did not acknowledge stop")
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel2()
		if out, e := c.Run(ctx2, request("After cancellation"), invoke); e != nil || out != "OK" {
			t.Fatalf("next task: %s %v", out, e)
		}
	})
	t.Run("budget", func(t *testing.T) {
		r := request("BUDGETTEST")
		r.MaxIterations = 2
		before := calls.Load()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := c.Run(ctx, r, invoke)
		if err == nil {
			t.Fatal("budget should terminate")
		}
		if n := calls.Load() - before; n != 2 {
			t.Fatalf("budget calls=%d", n)
		}
	})
}
