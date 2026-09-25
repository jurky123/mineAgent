package main

import (
	"context"
	"encoding/json"
	"fmt"

	"mineagent/internal/games"
	"mineagent/internal/portal"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

// registerPortalApps 往门户里注册应用/卡片。
// 新增门户功能 = 在这里加一条（或注册新应用），不改门户骨架。
func registerPortalApps(p *portal.Server, store *storage.Store, gw *tools.Gateway, gamesMgr *games.Manager) {
	p.Register(
		portal.App{
			ID: "announcements", Name: "公告栏", Icon: "📢", Order: 0, Enabled: true,
			Card: func(ctx context.Context, u *storage.User) (any, error) {
				list, err := store.ListAnnouncements(ctx, 5)
				if err != nil {
					return nil, err
				}
				items := make([]map[string]any, 0, len(list))
				for _, a := range list {
					items = append(items, map[string]any{
						"id": a.ID, "text": a.Text, "author": a.Author, "createdAt": a.CreatedAt,
					})
				}
				return map[string]any{
					"type": "list", "title": "公告栏", "items": items,
					"canEdit": u.IsAdmin, "empty": "还没有公告",
				}, nil
			},
		},
		portal.App{
			ID: "agent", Name: "Agent 对话", Desc: "写代码、查资料、收发文件", Icon: "🤖",
			Path: "/agent", Order: 10, Enabled: true,
			Card: func(ctx context.Context, u *storage.User) (any, error) {
				n, err := store.CountConversationsByUser(ctx, u.ID, u.Username)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"type": "stat", "title": "Agent 对话",
					"value": n, "hint": "个会话", "path": "/agent",
				}, nil
			},
		},
		portal.App{
			ID: "minecraft", Name: "Minecraft 服务器", Desc: "在线状态", Icon: "⛏️",
			Order: 20, Enabled: true,
			Card: mcStatusCard(gw),
		},
		portal.App{
			ID: "games", Name: "小游戏", Desc: "国际象棋 · 在线房间对战", Icon: "🎮",
			Path: "/games", Order: 30, Enabled: true,
			Card: func(ctx context.Context, u *storage.User) (any, error) {
				open := gamesMgr.OpenRooms()
				items := []map[string]any{
					{"label": "国际象棋", "value": "在线对战"},
					{"label": "开放房间", "value": fmt.Sprintf("%d", len(open))},
				}
				return map[string]any{
					"type": "stat", "title": "小游戏", "items": items,
					"hint": "带你朋友来开一局", "path": "/games",
				}, nil
			},
		},
	)
}

// mcStatusCard 经网关拿 MC 状态（结构化 JSON），失败时门户会显示"暂不可用"。
func mcStatusCard(gw *tools.Gateway) portal.CardFunc {
	return func(ctx context.Context, _ *storage.User) (any, error) {
		raw, err := gw.Call(ctx, "minecraft_server_status", nil)
		if err != nil {
			return nil, err
		}
		var st struct {
			TPS1m        float64 `json:"tps1m"`
			Online       int     `json:"online"`
			MaxPlayers   int     `json:"maxPlayers"`
			UsedMemoryMB int     `json:"usedMemoryMB"`
			MaxMemoryMB  int     `json:"maxMemoryMB"`
			Version      string  `json:"version"`
		}
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			return nil, fmt.Errorf("解析状态失败: %w", err)
		}
		items := []map[string]any{
			{"label": "在线", "value": fmt.Sprintf("%d / %d", st.Online, st.MaxPlayers)},
			{"label": "TPS", "value": fmt.Sprintf("%.2f", st.TPS1m)},
			{"label": "内存", "value": fmt.Sprintf("%d / %d MB", st.UsedMemoryMB, st.MaxMemoryMB)},
			{"label": "版本", "value": st.Version},
		}
		return map[string]any{
			"type": "stat", "title": "Minecraft 服务器",
			"items": items, "hint": "数据来自 Paper 服务器",
		}, nil
	}
}
