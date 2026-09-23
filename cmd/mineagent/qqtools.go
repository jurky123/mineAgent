package main

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

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
//   - 只读工具：直接透传。
//
// MC 通道不经过这里，行为与原来完全一致。
type qqToolGate struct {
	inner tool.BaseTool
	ws    *tools.Workspace
	store *storage.Store
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
	return g.inner.(tool.InvokableTool).InvokableRun(ctx, argsJSON, opts...)
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
		if name, err := store.LinkedMC(ctx, "qq", id); err == nil && name != "" {
			return name
		}
	}
	return ""
}
