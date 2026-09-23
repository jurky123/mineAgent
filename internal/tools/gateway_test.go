package tools

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"mineagent/internal/protocol"
)

type fakeSender struct {
	mu     sync.Mutex
	calls  []protocol.ToolCall
	onCall func(protocol.ToolCall)
	gw     *Gateway
}

func (f *fakeSender) SendProtocol(typ string, data any) error {
	call, ok := data.(protocol.ToolCall)
	if !ok {
		return nil
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	hook := f.onCall
	f.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	return nil
}

func (f *fakeSender) first() (protocol.ToolCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return protocol.ToolCall{}, false
	}
	return f.calls[0], true
}

func testGateway(t *testing.T) (*Gateway, *fakeSender) {
	t.Helper()
	sender := &fakeSender{}
	gw := NewGateway(sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	gw.timeout = time.Second
	return gw, sender
}

func TestCallSuccess(t *testing.T) {
	gw, sender := testGateway(t)
	go func() {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if call, ok := sender.first(); ok {
				gw.HandleResult(protocol.ToolResult{
					CallID: call.CallID,
					OK:     true,
					Data:   json.RawMessage(`{"count":1}`),
				})
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	out, err := gw.Call(context.Background(), "minecraft_list_players", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"count":1}` {
		t.Fatalf("out = %s", out)
	}
	call, _ := sender.first()
	if call.Tool != "minecraft_list_players" {
		t.Fatalf("tool = %s", call.Tool)
	}
	if string(call.Args) != `{}` {
		t.Fatalf("args = %s", call.Args)
	}
}

func TestCallErrorResult(t *testing.T) {
	gw, sender := testGateway(t)
	go func() {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if call, ok := sender.first(); ok {
				gw.HandleResult(protocol.ToolResult{CallID: call.CallID, OK: false, Error: "player offline"})
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	if _, err := gw.Call(context.Background(), "minecraft_player_info", json.RawMessage(`{"player":"Steve"}`)); err == nil {
		t.Fatal("expected error")
	}
}

func TestCallTimeout(t *testing.T) {
	gw, _ := testGateway(t)
	gw.timeout = 50 * time.Millisecond
	if _, err := gw.Call(context.Background(), "minecraft_list_players", nil); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestCallContextCancel(t *testing.T) {
	gw, _ := testGateway(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gw.Call(ctx, "minecraft_list_players", nil); err == nil {
		t.Fatal("expected context error")
	}
}

func TestHandleUnknownResult(t *testing.T) {
	gw, _ := testGateway(t)
	gw.HandleResult(protocol.ToolResult{CallID: "nope", OK: true})
}

func TestToolFailureReturnedToModel(t *testing.T) {
	gw, _ := testGateway(t)
	gw.timeout = 50 * time.Millisecond
	ts := ReadOnly(gw)
	inv, ok := ts[1].(tool.InvokableTool)
	if !ok {
		t.Fatal("player_info is not invokable")
	}
	out, err := inv.InvokableRun(context.Background(), `{"player":"Steve"}`)
	if err != nil {
		t.Fatalf("tool error should be returned as content, got err=%v", err)
	}
	if !strings.Contains(out, `"error"`) {
		t.Fatalf("out = %s", out)
	}
}

func TestReadOnlyTools(t *testing.T) {
	gw, _ := testGateway(t)
	ts := ReadOnly(gw)
	if len(ts) != 7 {
		t.Fatalf("tools = %d", len(ts))
	}
	names := map[string]bool{}
	for _, tl := range ts {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		names[info.Name] = true
	}
	for _, want := range []string{
		"minecraft_list_players",
		"minecraft_player_info",
		"minecraft_server_status",
		"minecraft_world_time",
		"minecraft_weather",
		"minecraft_world_info",
		"minecraft_plugin_list",
	} {
		if !names[want] {
			t.Fatalf("missing tool %s", want)
		}
	}
}
