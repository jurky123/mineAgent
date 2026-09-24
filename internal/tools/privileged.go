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

	"mineagent/internal/storage"
)

type requesterCtxKey struct{}

type requesterIDCtxKey struct{}

type sessionCtxKey struct{}

// mcRequesterCtxKey 只有 QQ 通道会设置：QQ 身份绑定的 MC 玩家名（可能为空=未绑定）。
// Gateway.Call 用它作为发给 MC 插件的 requester；
// audit 用 qq:<openid>（见 WithRequester），两者不混淆。
type mcRequesterCtxKey struct{}

func WithRequester(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, requesterCtxKey{}, name)
}

func RequesterFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requesterCtxKey{}).(string)
	return v
}

// WithMCRequester 供 QQ 通道设置绑定的 MC 玩家名。
func WithMCRequester(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, mcRequesterCtxKey{}, name)
}

func MCRequesterFromContext(ctx context.Context) string {
	v, _ := ctx.Value(mcRequesterCtxKey{}).(string)
	return v
}

func WithRequesterID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requesterIDCtxKey{}, id)
}

func RequesterIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requesterIDCtxKey{}).(string)
	return v
}

func WithSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, sessionID)
}

func sessionFromContext(ctx context.Context) string {
	v, _ := ctx.Value(sessionCtxKey{}).(string)
	return v
}

// replyTargetCtxKey 存本次对话的回执目标（agent.Request.ReplyTarget）。
// respond/resume 在 run 前放进去，工具链（qqToolGate->qq_markdown/qq_image）
// 从这里取，保证 agent 只能发回当前会话，不能跨会话发。
type replyTargetCtxKey struct{}

// WithReplyTarget 供 agent.respond/resume 把 Request.ReplyTarget 放进 ctx。
func WithReplyTarget(ctx context.Context, target string) context.Context {
	return context.WithValue(ctx, replyTargetCtxKey{}, target)
}

// ReplyTargetFromContext 取本次对话的回执目标（""=MC 纯文本广播语义）。
func ReplyTargetFromContext(ctx context.Context) string {
	v, _ := ctx.Value(replyTargetCtxKey{}).(string)
	return v
}

// SessionFromContext 供 agent 摘要中间件从 ctx 里取当前会话，
// 这样 MC/QQ 共用一套中间件逻辑也能把摘要存到各自会话下。
func SessionFromContext(ctx context.Context) string {
	return sessionFromContext(ctx)
}

const (
	internalCheckCommand    = "internal_check_command"
	internalCheckPermission = "internal_check_permission"
)

func checkPermission(ctx context.Context, gw *Gateway, permission string) (bool, string, error) {
	raw, err := json.Marshal(map[string]string{"permission": permission})
	if err != nil {
		return false, "", err
	}
	out, err := gw.Call(ctx, internalCheckPermission, raw)
	if err != nil {
		return false, "", err
	}
	var res struct {
		Allowed bool   `json:"allowed"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return false, "", err
	}
	return res.Allowed, res.Reason, nil
}

func checkCommandPermission(ctx context.Context, gw *Gateway, args map[string]any) (bool, string, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return false, "", err
	}
	out, err := gw.Call(ctx, internalCheckCommand, raw)
	if err != nil {
		return false, "", err
	}
	var res struct {
		Allowed    bool   `json:"allowed"`
		Permission string `json:"permission"`
		Reason     string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return false, "", err
	}
	if !res.Allowed {
		if res.Permission == "" && res.Reason != "" {
			return false, res.Reason, nil
		}
		return false, res.Permission, nil
	}
	return true, res.Permission, nil
}

type approvalTool struct {
	gw        *Gateway
	approvals *Approvals
	store     *storage.Store
	log       *slog.Logger

	name       string
	desc       string
	params     map[string]*schema.ParameterInfo
	risk       string
	permission string
	prompt     func(args map[string]any) string
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

	if t.permission != "" && !strings.HasPrefix(RequesterFromContext(ctx), QQRequesterPrefix) {
		allowed, reason, err := checkPermission(ctx, t.gw, t.permission)
		if err != nil {
			msg := "权限校验失败（无法联系 Minecraft 服务器），已拒绝执行"
			t.audit(ctx, argsJSON, "permission_check_failed", "", err.Error())
			return errorJSON(msg), nil
		}
		if !allowed {
			msg := "你没有执行该操作的权限（" + t.permission + "），我不能执行"
			if reason != "" {
				msg = reason + "，无法以本人身份执行该操作"
			}
			t.audit(ctx, argsJSON, "permission_denied", "", msg)
			return errorJSON(msg), nil
		}
	}

	if t.name == "minecraft_run_command" && !strings.HasPrefix(RequesterFromContext(ctx), QQRequesterPrefix) {
		allowed, detail, err := checkCommandPermission(ctx, t.gw, args)
		if err != nil {
			msg := "权限校验失败（无法联系 Minecraft 服务器），已拒绝执行"
			t.audit(ctx, argsJSON, "permission_check_failed", "", err.Error())
			return errorJSON(msg), nil
		}
		if !allowed {
			msg := detail + "，我不能代为执行"
			if strings.Contains(detail, ".") && !strings.Contains(detail, " ") {
				msg = "你没有执行该命令的权限（" + detail + "），我不能代为执行"
			}
			t.audit(ctx, argsJSON, "permission_denied", "", msg)
			return errorJSON(msg), nil
		}
	}

	info, err := t.approvals.Create(ApprovalInfo{
		Tool:      t.name,
		Args:      json.RawMessage(argsJSON),
		Requester: RequesterFromContext(ctx),
		Prompt:    t.prompt(args),
	})
	if err != nil {
		t.audit(ctx, argsJSON, "policy_denied", "", err.Error())
		return errorJSON(err.Error()), nil
	}
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

func Privileged(gw *Gateway, approvals *Approvals, store *storage.Store, log *slog.Logger) []tool.BaseTool {
	return []tool.BaseTool{
		&approvalTool{
			gw: gw, approvals: approvals, store: store, log: log,
			name:       "minecraft_teleport",
			desc:       "将一名在线玩家传送到另一名在线玩家处。属于高权限操作，会先请求管理员批准。",
			risk:       "high",
			permission: "mineagent.teleport",
			params: map[string]*schema.ParameterInfo{
				"player": {Type: schema.String, Desc: "被传送的玩家名", Required: true},
				"target": {Type: schema.String, Desc: "目标玩家名", Required: true},
			},
			prompt: func(args map[string]any) string {
				return fmt.Sprintf("把玩家 %v 传送到玩家 %v 身边", args["player"], args["target"])
			},
		},
		&approvalTool{
			gw: gw, approvals: approvals, store: store, log: log,
			name:       "minecraft_give",
			desc:       "给予在线玩家物品。属于高权限操作，会先请求管理员批准。",
			risk:       "high",
			permission: "mineagent.give",
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
			gw: gw, approvals: approvals, store: store, log: log,
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
