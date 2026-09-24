package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

// qqToolGate 是 QQ 通道专用的工具包装：调用前按 QQ 身份往 ctx 里追加门禁信息。
//   - workspace_*：按 openid 是否命中 adminOpenIDs 放 WithQQAdmin（工具内执行时检查）。
//   - qq_bind/qq_unbind：放 WithQQIdentity（openid 候选，供绑定读写用）。
//   - minecraft_teleport/give/run_command：
//     requester 已是 qq:<openid>（channel.go 放的），approvalTool 凭此前缀跳过
//     "本人权限预检"直接转游戏内审批；WithMCRequester 补上绑定的 MC 名，
//     供 Gateway.Call 发给插件执行、以及 resume 后继续执行。
//   - qq_markdown/qq_image：agent.respond 已把 ReplyTarget 放进 ctx，
//     工具凭它只能发回当前会话；工具返回 __qq_send 指令后，gate 调 sender
//     发出并记库（sendQQ）。
//   - 只读工具：直接透传。
//
// MC 通道不经过这里，行为与原来完全一致。
type qqToolGate struct {
	inner tool.BaseTool
	ws    *tools.Workspace
	store *storage.Store
	// sender 发 qq_markdown/qq_image 用：调 Session.Reply 走 fanout（记库+channel.Send）。
	// sessions 按 sessionKey 取 Session（hub.Session 的薄包装，见 main.go 装配）。
	sender   func(ctx context.Context, sess *session.Session, target, text string) error
	sessions func(ctx context.Context, sessionKey string) (*session.Session, error)
}

func (g *qqToolGate) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return g.inner.Info(ctx)
}

func (g *qqToolGate) InvokableRun(ctx context.Context, argsJSON string, opts ...tool.Option) (string, error) {
	info, err := g.inner.Info(ctx)
	if err != nil {
		return "", err
	}
	requester := tools.RequesterFromContext(ctx)
	openIDs := openIDCandidates(ctx, requester)

	switch {
	case strings.HasPrefix(info.Name, "workspace_"):
		ctx = tools.WithQQAdmin(ctx, g.ws.IsAdmin(openIDs...))
	case info.Name == "qq_bind" || info.Name == "qq_unbind":
		ctx = tools.WithQQIdentity(ctx, openIDs)
	case info.Name == "minecraft_teleport" || info.Name == "minecraft_give" || info.Name == "minecraft_run_command":
		if mcName := lookupBoundMC(ctx, g.store, openIDs); mcName != "" {
			ctx = tools.WithMCRequester(ctx, mcName)
		}
	}
	out, err := g.inner.(tool.InvokableTool).InvokableRun(ctx, argsJSON, opts...)
	if err != nil {
		return "", err
	}
	// qq_markdown/qq_image 返回 __qq_send 指令：gate 执行发送。
	if info.Name == "qq_markdown" || info.Name == "qq_image" {
		var cmd map[string]string
		if json.Unmarshal([]byte(out), &cmd) == nil && cmd[tools.QQSEND_KEY] != "" {
			target, text := cmd["target"], cmd["text"]
			if target == "" {
				return errorJSON("发送目标为空，已取消"), nil
			}
			if err := g.sendQQ(ctx, target, text); err != nil {
				return errorJSON("发送失败："+err.Error()), nil
			}
			ok, _ := json.Marshal(map[string]string{"ok": "true", "message": "已发送"})
			return string(ok), nil
		}
	}
	return out, nil
}

// sendQQ 按指令 target 发出：
//   - md: 前缀走 markdown，img: 前缀走图片（media.go 分片上传）。
//   - 校验 base（去前缀、去 path 后的 "<c2c|group>:<id>:<msgID>"）必须 ==
//     ctx 里的 ReplyTarget（防 agent 伪造跨会话发送）。
//   - 图片 path 做 workspace 相对路径校验（防 ../../../etc 之类，
//     真正的 resolve 钳制在 media.go 读文件前再做一次）。
func (g *qqToolGate) sendQQ(ctx context.Context, target, text string) error {
	inner := target
	if strings.HasPrefix(target, storage.KindMarkdown) {
		inner = strings.TrimPrefix(target, storage.KindMarkdown)
	} else if strings.HasPrefix(target, storage.KindImage) {
		inner = strings.TrimPrefix(target, storage.KindImage)
	} else {
		return errors.New("未知发送类型")
	}
	base, relPath := splitSendInner(inner)
	if base == "" {
		return errors.New("发送目标非法，已取消")
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

// splitSendInner 切 "<c2c|group>:<id>:<msgID>[:<path>]"。
// 返回 base="<c2c|group>:<id>:<msgID>" 和 path（md 无 path）。
func splitSendInner(inner string) (base, relPath string) {
	parts := strings.SplitN(inner, ":", 4)
	if len(parts) < 3 || (parts[0] != "c2c" && parts[0] != "group") {
		return "", ""
	}
	if len(parts) == 3 {
		return inner, ""
	}
	return strings.Join(parts[:3], ":"), parts[3]
}

// checkWorkspaceRelPath 图片 path 初验：相对路径、不含 .. 逃逸、不为空。
func checkWorkspaceRelPath(p string) error {
	if p == "" {
		return errors.New("图片路径为空")
	}
	if filepath.IsAbs(p) {
		return errors.New("图片路径只能是 workspace 内相对路径")
	}
	if cleaned := filepath.Clean(p); cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return errors.New("图片路径不能逃出 workspace")
	}
	return nil
}

func errorJSON(msg string) string {
	raw, _ := json.Marshal(map[string]string{"error": msg})
	return string(raw)
}

// openIDCandidates 从 requester(qq:<union|user|member openid> 三选一存的第一个非空）
// 还原 openid。channel.go 只存了一个最优先的非空 id，这里能还原的就是那一个；
// 足够做管理员判断和绑定读写（绑定时三个 id 全写，查时按优先级逐个查）。
func openIDCandidates(ctx context.Context, requester string) []string {
	id := strings.TrimPrefix(requester, tools.QQRequesterPrefix)
	if id == "" || id == requester {
		// 不是 qq: 前缀（理论上 QQ 通道不会走到这里），requesterID 兜底。
		if rid := tools.RequesterIDFromContext(ctx); rid != "" {
			return []string{rid}
		}
		return nil
	}
	return []string{id}
}

func lookupBoundMC(ctx context.Context, store *storage.Store, openIDs []string) string {
	for _, id := range openIDs {
		if id == "" {
			continue
		}
		// 注意用 qq_bind 而不是 qq：后者是昵称留痕，会把"QQ用户"误认成绑定。
		if name, err := store.LinkedMC(ctx, tools.BindPlatform, id); err == nil && name != "" {
			return name
		}
	}
	return ""
}
