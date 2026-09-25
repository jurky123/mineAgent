package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"mineagent/internal/games"
	"mineagent/internal/portal"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

// registerPortalApps 往门户里注册应用/卡片。
// 新增门户功能 = 在这里加一条（或注册新应用），不改门户骨架。
//
// HomeRole 决定首页位置：hero（一级入口，要大）/ status（服务器状态）/
// content（内容区）/ feed（动态流）。Nav 决定是否出现在顶栏。
func registerPortalApps(p *portal.Server, store *storage.Store, gw *tools.Gateway, gamesMgr *games.Manager) {
	p.Register(
		portal.App{
			ID: "agent", Name: "MineAgent", Desc: "写代码、查资料、收发文件", Icon: "sparkle",
			Path: "/agent", Order: 10, Enabled: true,
			Nav: true, NavLabel: "Agent", HomeRole: "hero", Priority: 10, Span: 2,
			Card: agentHeroCard(store),
		},
		portal.App{
			ID: "minecraft", Name: "Minecraft", Desc: "服务器状态", Icon: "pickaxe",
			Order: 20, Enabled: true, Nav: false, HomeRole: "status", Priority: 20,
			Card: mcStatusCard(gw),
		},
		portal.App{
			ID: "games", Name: "游戏", Desc: "和朋友玩点什么", Icon: "gamepad",
			Path: "/games", Order: 30, Enabled: true,
			Nav: true, HomeRole: "content", Priority: 30,
			Card: gamesCard(store, gamesMgr),
		},
		portal.App{
			ID: "announcements", Name: "最新动态", Icon: "megaphone",
			Order: 40, Enabled: true, Nav: false, HomeRole: "feed", Priority: 40, Span: 2,
			Card: announcementsFeed(store),
		},
	)
}

// agentHeroCard 首页一级入口：最近一次对话 + 会话数。
func agentHeroCard(store *storage.Store) portal.CardFunc {
	return func(ctx context.Context, u *storage.User) (any, error) {
		list, err := store.ListConversationsByUser(ctx, u.ID, u.Username)
		if err != nil {
			return nil, err
		}
		out := map[string]any{
			"type":     "hero",
			"title":    "MineAgent",
			"newPath":  "/agent?new=1",
			"path":     "/agent",
			"count":    len(list),
			"subtitle": "写代码、查资料、收发文件、控制服务器",
		}
		if len(list) > 0 && list[0].Conv != "" {
			out["lastTitle"] = list[0].Title
			out["lastUpdated"] = list[0].UpdatedAt
			out["continuePath"] = "/agent?conv=" + list[0].Conv
		} else if len(list) > 0 {
			out["lastTitle"] = list[0].Title
			out["lastUpdated"] = list[0].UpdatedAt
			out["continuePath"] = "/agent"
		} else {
			out["continuePath"] = "/agent"
		}
		return out, nil
	}
}

// mcStatusCard 服务器状态：开着吗？谁在玩？现在要不要进去？（细节收进 details）
func mcStatusCard(gw *tools.Gateway) portal.CardFunc {
	return func(ctx context.Context, _ *storage.User) (any, error) {
		var (
			wg         sync.WaitGroup
			mu         sync.Mutex
			statusRaw  string
			statusErr  error
			playersRaw string
			weatherRaw string
			timeRaw    string
		)
		call := func(dst *string, name string) {
			defer wg.Done()
			raw, err := gw.Call(ctx, name, nil)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if name == "minecraft_server_status" {
					statusErr = err
				}
				return
			}
			*dst = raw
		}
		wg.Add(4)
		go call(&statusRaw, "minecraft_server_status")
		go call(&playersRaw, "minecraft_list_players")
		go call(&weatherRaw, "minecraft_weather")
		go call(&timeRaw, "minecraft_world_time")
		wg.Wait()
		if statusErr != nil {
			return nil, statusErr
		}

		var st struct {
			TPS1m        float64 `json:"tps1m"`
			TPS5m        float64 `json:"tps5m"`
			TPS15m       float64 `json:"tps15m"`
			Online       int     `json:"online"`
			MaxPlayers   int     `json:"maxPlayers"`
			UsedMemoryMB int     `json:"usedMemoryMB"`
			MaxMemoryMB  int     `json:"maxMemoryMB"`
			Version      string  `json:"version"`
		}
		if err := json.Unmarshal([]byte(statusRaw), &st); err != nil {
			return nil, fmt.Errorf("解析服务器状态失败: %w", err)
		}
		names := []string{}
		if playersRaw != "" {
			var pl struct {
				Players []struct {
					Name string `json:"name"`
				} `json:"players"`
			}
			if json.Unmarshal([]byte(playersRaw), &pl) == nil {
				for _, p := range pl.Players {
					names = append(names, p.Name)
				}
			}
		}
		weather, period := "", ""
		if weatherRaw != "" {
			var w struct {
				Worlds []struct {
					World      string `json:"world"`
					Raining    bool   `json:"raining"`
					Thundering bool   `json:"thundering"`
				} `json:"worlds"`
			}
			if json.Unmarshal([]byte(weatherRaw), &w) == nil && len(w.Worlds) > 0 {
				world := w.Worlds[0]
				for _, x := range w.Worlds {
					if x.World == "world" {
						world = x
						break
					}
				}
				switch {
				case world.Thundering:
					weather = "雷暴"
				case world.Raining:
					weather = "下雨"
				default:
					weather = "晴天"
				}
			}
		}
		if timeRaw != "" {
			var t struct {
				Worlds []struct {
					World  string `json:"world"`
					Period string `json:"period"`
				} `json:"worlds"`
			}
			if json.Unmarshal([]byte(timeRaw), &t) == nil && len(t.Worlds) > 0 {
				period = t.Worlds[0].Period
				for _, x := range t.Worlds {
					if x.World == "world" {
						period = x.Period
						break
					}
				}
			}
		}
		return map[string]any{
			"type": "status", "title": "Minecraft",
			"online": true, "count": st.Online, "max": st.MaxPlayers,
			"players": names, "tps": st.TPS1m, "weather": weather, "period": period,
			"details": []map[string]any{
				{"label": "内存", "value": fmt.Sprintf("%d / %d MB", st.UsedMemoryMB, st.MaxMemoryMB)},
				{"label": "版本", "value": st.Version},
				{"label": "TPS 5m/15m", "value": fmt.Sprintf("%.2f / %.2f", st.TPS5m, st.TPS15m)},
			},
		}, nil
	}
}

// gamesCard 首页内容区：开放房间 + 我的胜负。
func gamesCard(store *storage.Store, mgr *games.Manager) portal.CardFunc {
	return func(ctx context.Context, u *storage.User) (any, error) {
		out := map[string]any{
			"type": "game", "game": "chess", "name": "国际象棋",
			"path": "/games/chess", "openRooms": len(mgr.OpenRooms()),
		}
		if stats, err := store.GameStats(ctx, u.ID); err == nil {
			for _, st := range stats {
				if st.GameID == "chess" {
					out["wins"], out["losses"], out["draws"] = st.Wins, st.Losses, st.Draws
				}
			}
		}
		return out, nil
	}
}

// announcementsFeed 首页动态流：最新 3 条 + 管理员发布入口。
func announcementsFeed(store *storage.Store) portal.CardFunc {
	return func(ctx context.Context, u *storage.User) (any, error) {
		list, err := store.ListAnnouncements(ctx, 3)
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
			"type": "feed", "title": "最新动态", "items": items,
			"canEdit": u.IsAdmin, "empty": "还没有动态",
		}, nil
	}
}
