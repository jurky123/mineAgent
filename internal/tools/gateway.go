package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"mineagent/internal/protocol"
)

type Sender interface {
	SendProtocol(typ string, data any) error
}

type Gateway struct {
	sender  Sender
	log     *slog.Logger
	timeout time.Duration

	mu      sync.Mutex
	pending map[string]chan protocol.ToolResult
	seq     atomic.Uint64
}

func NewGateway(sender Sender, log *slog.Logger) *Gateway {
	return &Gateway{
		sender:  sender,
		log:     log,
		timeout: 15 * time.Second,
		pending: make(map[string]chan protocol.ToolResult),
	}
}

func (g *Gateway) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if g.sender == nil {
		return "", errors.New("tool gateway: no minecraft connection")
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	callID := fmt.Sprintf("%d-%d", time.Now().UnixMilli(), g.seq.Add(1))
	ch := make(chan protocol.ToolResult, 1)
	g.mu.Lock()
	g.pending[callID] = ch
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.pending, callID)
		g.mu.Unlock()
	}()

	if err := g.sender.SendProtocol(protocol.TypeToolCall, protocol.ToolCall{
		CallID:        callID,
		Tool:          name,
		Args:          args,
		Requester:     RequesterFromContext(ctx),
		RequesterUUID: RequesterIDFromContext(ctx),
		TimeoutMS:     g.timeout.Milliseconds(),
	}); err != nil {
		return "", fmt.Errorf("call %s: %w", name, err)
	}
	g.log.Info("tool call sent", "tool", name, "callId", callID)

	timer := time.NewTimer(g.timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		g.log.Warn("tool call canceled", "tool", name, "callId", callID, "err", ctx.Err())
		return "", ctx.Err()
	case <-timer.C:
		g.log.Warn("tool call timeout", "tool", name, "callId", callID)
		return "", fmt.Errorf("tool %s timed out after %s", name, g.timeout)
	case res := <-ch:
		if !res.OK {
			g.log.Warn("tool call failed", "tool", name, "callId", callID, "err", res.Error)
			msg := res.Error
			if msg == "" {
				msg = "unknown error"
			}
			return "", fmt.Errorf("tool %s failed: %s", name, msg)
		}
		g.log.Info("tool call done", "tool", name, "callId", callID)
		out := string(res.Data)
		if out == "" {
			out = "{}"
		}
		return out, nil
	}
}

func (g *Gateway) HandleResult(res protocol.ToolResult) {
	g.mu.Lock()
	ch, ok := g.pending[res.CallID]
	if ok {
		delete(g.pending, res.CallID)
	}
	g.mu.Unlock()
	if !ok {
		g.log.Warn("tool result for unknown call", "callId", res.CallID)
		return
	}
	select {
	case ch <- res:
	default:
	}
}
