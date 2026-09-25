package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

// wecomToolGate 是企微通道专用工具包装（与 qqToolGate 同构）：
//   - workspace_*：按 userid 是否命中 adminUserIds 放 WithQQAdmin（工具内检查）。
//   - qq_bind/qq_unbind（工具名沿用，语义是"绑定 MC 身份"）：放 WithQQIdentity。
//   - minecraft_teleport/give/run_command：requester 已是 wecom:<userid>，
//     approvalTool 凭外部前缀跳过本人预检直转游戏内审批；WithMCRequester 带绑定 MC 名。
//   - wecom_markdown/wecom_image：工具返回 __wecom_send 指令后，gate 走 sender 发出。
//   - 只读工具：直接透传。
type wecomToolGate struct {
	inner    tool.BaseTool
	ws       *tools.Workspace
	store    *storage.Store
	sender   func(ctx context.Context, sess *session.Session, target, text string) error
	sessions func(ctx context.Context, sessionKey string) (*session.Session, error)
}

func (g *wecomToolGate) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return g.inner.Info(ctx)
}

func (g *wecomToolGate) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	info, err := g.inner.Info(ctx)
	if err != nil {
		return "", err
	}
	userID := strings.TrimPrefix(tools.RequesterFromContext(ctx), tools.WeComRequesterPrefix)

	switch {
	case strings.HasPrefix(info.Name, "workspace_"):
		ctx = tools.WithQQAdmin(ctx, g.ws.IsAdmin(userID))
	case info.Name == "qq_bind" || info.Name == "qq_unbind":
		ctx = tools.WithQQIdentity(ctx, []string{userID})
	case info.Name == "minecraft_teleport" || info.Name == "minecraft_give" || info.Name == "minecraft_run_command":
		if mcName := lookupBoundMC(ctx, g.store, []string{userID}); mcName != "" {
			ctx = tools.WithMCRequester(ctx, mcName)
		}
	}
	out, err := g.inner.(tool.InvokableTool).InvokableRun(ctx, argsJSON, opts...)
	if err != nil {
		return "", err
	}
	if info.Name == "wecom_markdown" || info.Name == "wecom_image" {
		var cmd map[string]string
		if json.Unmarshal([]byte(out), &cmd) == nil && cmd[tools.WECOMSEND_KEY] != "" {
			target, text := cmd["target"], cmd["text"]
			if target == "" {
				return errorJSON("发送目标为空，已取消"), nil
			}
			if err := g.sendWeCom(ctx, target, text); err != nil {
				return errorJSON("发送失败：" + err.Error()), nil
			}
			ok, _ := json.Marshal(map[string]string{"ok": "true", "message": "已发送"})
			return string(ok), nil
		}
	}
	return out, nil
}

// sendWeCom 校验 target 与 ReplyTarget 一致（只能发回当前会话），
// 图片路径初验后交 sender 走 session fanout。
func (g *wecomToolGate) sendWeCom(ctx context.Context, target, text string) error {
	inner := target
	if strings.HasPrefix(target, storage.KindMarkdown) {
		inner = strings.TrimPrefix(target, storage.KindMarkdown)
	} else if strings.HasPrefix(target, storage.KindImage) {
		inner = strings.TrimPrefix(target, storage.KindImage)
	} else {
		return errors.New("未知发送类型")
	}
	parts := strings.SplitN(inner, ":", 3)
	if len(parts) < 2 || (parts[0] != "c2c" && parts[0] != "group") {
		return errors.New("发送目标非法，已取消")
	}
	base := parts[0] + ":" + parts[1]
	relPath := ""
	if len(parts) == 3 {
		relPath = parts[2]
	}
	if want := tools.QQReplyTargetFromContext(ctx); want == "" || base != want {
		return errors.New("只能发回当前会话，已取消")
	}
	if relPath != "" {
		if err := checkWorkspaceRelPath(relPath); err != nil {
			return err
		}
	}
	if g.sessions == nil || g.sender == nil {
		return errors.New("发送器未就绪，稍后再试")
	}
	sessionKey := tools.SessionFromContext(ctx)
	sess, err := g.sessions(ctx, sessionKey)
	if err != nil {
		return err
	}
	return g.sender(ctx, sess, target, text)
}
