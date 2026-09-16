package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"mineagent/internal/config"
	"mineagent/internal/storage"
)

type requesterCtxKey struct{}

type sessionCtxKey struct{}

func WithRequester(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, requesterCtxKey{}, name)
}

func RequesterFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requesterCtxKey{}).(string)
	return v
}

func WithSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, sessionID)
}

func sessionFromContext(ctx context.Context) string {
	v, _ := ctx.Value(sessionCtxKey{}).(string)
	return v
}

type approvalTool struct {
	gw        *Gateway
	approvals *Approvals
	store     *storage.Store
	log       *slog.Logger
	cfg       config.Tools

	name   string
	desc   string
	params map[string]*schema.ParameterInfo
	risk   string
	prompt func(args map[string]any) string
}

func (t *approvalTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{Name: t.name, Desc: t.desc}
	if len(t.params) > 0 {
		info.ParamsOneOf = schema.NewParamsOneOfByParams(t.params)
	}
	return info, nil
}

func (t *approvalTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	if argsJSON == "" {
		argsJSON = "{}"
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return errorJSON("参数不是合法 JSON"), nil
	}

	isResume, hasData, decision := compose.GetResumeContext[*ApprovalDecision](ctx)
	if isResume && hasData && decision != nil {
		if !decision.Approved {
			reason := decision.Reason
			if reason == "" {
				reason = "管理员拒绝"
			}
			t.audit(ctx, argsJSON, "denied", decision.Operator, reason)
			return errorJSON("操作未获批准：" + reason), nil
		}
		out, err := t.gw.Call(ctx, t.name, json.RawMessage(argsJSON))
		if err != nil {
			t.audit(ctx, argsJSON, "approved", decision.Operator, err.Error())
			return errorJSON(err.Error()), nil
		}
		t.audit(ctx, argsJSON, "approved", decision.Operator, truncate(out, 500))
		return out, nil
	}

	if err := t.validate(args); err != nil {
		t.audit(ctx, argsJSON, "policy_denied", "", err.Error())
		return errorJSON(err.Error()), nil
	}

	info := t.approvals.Create(ApprovalInfo{
		Tool:      t.name,
		Args:      json.RawMessage(argsJSON),
		Requester: RequesterFromContext(ctx),
		Prompt:    t.prompt(args),
	})
	return "", compose.Interrupt(ctx, &info)
}

func (t *approvalTool) validate(args map[string]any) error {
	switch t.name {
	case "minecraft_run_command":
		cmd, _ := args["command"].(string)
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cmd), "/")) == "" {
			return fmt.Errorf("命令为空")
		}
	case "minecraft_give":
		if count, ok := args["count"].(float64); ok && (count < 1 || count > 64) {
			return fmt.Errorf("数量需在 1-64 之间")
		}
	}
	return nil
}

func (t *approvalTool) audit(ctx context.Context, argsJSON, decision, operator, result string) {
	entry := storage.AuditEntry{
		SessionID: sessionFromContext(ctx),
		Tool:      t.name,
		Risk:      t.risk,
		Requester: RequesterFromContext(ctx),
		Args:      argsJSON,
		Decision:  decision,
		Operator:  operator,
		Result:    truncate(result, 1000),
		CreatedAt: time.Now().UnixMilli(),
	}
	if _, err := t.store.SaveAudit(ctx, entry); err != nil {
		t.log.Warn("audit write failed", "err", err, "tool", t.name)
	}
}

func Privileged(gw *Gateway, approvals *Approvals, store *storage.Store, cfg config.Tools, log *slog.Logger) []tool.BaseTool {
	return []tool.BaseTool{
		&approvalTool{
			gw: gw, approvals: approvals, store: store, log: log, cfg: cfg,
			name: "minecraft_teleport",
			desc: "将一名在线玩家传送到另一名在线玩家处。属于高权限操作，会先请求管理员批准。",
			risk: "high",
			params: map[string]*schema.ParameterInfo{
				"player": {Type: schema.String, Desc: "被传送的玩家名", Required: true},
				"target": {Type: schema.String, Desc: "目标玩家名", Required: true},
			},
			prompt: func(args map[string]any) string {
				return fmt.Sprintf("把玩家 %v 传送到玩家 %v 身边", args["player"], args["target"])
			},
		},
		&approvalTool{
			gw: gw, approvals: approvals, store: store, log: log, cfg: cfg,
			name: "minecraft_give",
			desc: "给予在线玩家物品。属于高权限操作，会先请求管理员批准。",
			risk: "high",
			params: map[string]*schema.ParameterInfo{
				"player": {Type: schema.String, Desc: "玩家名", Required: true},
				"item":   {Type: schema.String, Desc: "物品 ID，如 diamond、minecraft:bread", Required: true},
				"count":  {Type: schema.Integer, Desc: "数量（1-64）", Required: true},
			},
			prompt: func(args map[string]any) string {
				return fmt.Sprintf("给予玩家 %v %v 个 %v", args["player"], args["count"], args["item"])
			},
		},
		&approvalTool{
			gw: gw, approvals: approvals, store: store, log: log, cfg: cfg,
			name: "minecraft_run_command",
			desc: "以请求者本人的身份执行 Minecraft 命令（命令权限由服务器权限组硬性决定）。属于高权限操作，会先请求管理员批准。",
			risk: "high",
			params: map[string]*schema.ParameterInfo{
				"command": {Type: schema.String, Desc: "不含前导 / 的命令，如 time set day", Required: true},
			},
			prompt: func(args map[string]any) string {
				cmd, _ := args["command"].(string)
				return fmt.Sprintf("以请求者身份执行命令：/%s", strings.TrimPrefix(strings.TrimSpace(cmd), "/"))
			},
		},
	}
}

func errorJSON(message string) string {
	payload, _ := json.Marshal(map[string]string{"error": message})
	return string(payload)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
