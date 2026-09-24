package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mineagent/internal/protocol"
)

// QQRequesterPrefix 是 QQ 侧发起 MC 高权限审批时的 requester 前缀，
// 格式为 qq:<openid>。WeCom 同理，前缀 wecom:<userid>。
// approvalTool 只做"本人身份"预检，对此外部身份一律
// 跳过玩家权限预检，直接走游戏内管理员审批（见 approvalTool.InvokableRun）。
const QQRequesterPrefix = "qq:"

// WeComRequesterPrefix 是 WeCom 侧的外部身份前缀，格式 wecom:<userid>。
const WeComRequesterPrefix = "wecom:"

// IsExternalRequester 判外部身份（QQ/WeCom）：跳过本人权限预检、直转游戏内审批。
func IsExternalRequester(requester string) bool {
	return strings.HasPrefix(requester, QQRequesterPrefix) ||
		strings.HasPrefix(requester, WeComRequesterPrefix)
}

type Sender interface {
	SendProtocol(typ string, data any) error
}

// ServerTool 由后端进程内直接实现、不走 MC 插件的工具统一实现这个接口。
// toolsGateway 只管 MC 插件 RPC；workspace 这类本地工具走 systemTools。
type ServerTool interface {
	InvokableRun(ctx context.Context, argsJSON string) (string, error)
}

// Gateway 是到 MC 插件的 tool.call RPC 网关。
// QQ 发起的 MC 高权限审批也要经由它发到游戏内审批，区别见 main.go 装配：
// MC 侧 requester=玩家名（插件内做 LuckPerms 预检），
// QQ 侧 requester=qq:<openid> 且 tool.call 里 requester=<绑定的MC名|空>。
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
	// QQ/WeCom 侧发起时 tool.call 的 requester 必须是绑定的 MC 名（或空=未绑定），
	// 不能把 qq:<openid>/wecom:<userid> 发给插件（插件按玩家名解析）。MC 侧保持原行为。
	toolRequester := RequesterFromContext(ctx)
	if IsExternalRequester(toolRequester) {
		toolRequester = MCRequesterFromContext(ctx)
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
		Requester:     toolRequester,
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
