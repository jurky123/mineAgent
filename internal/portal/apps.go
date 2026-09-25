package portal

import (
	"context"

	"mineagent/internal/storage"
)

// CardFunc 返回首页卡片数据；失败时门户降级显示"暂不可用"。
// 约定返回 map（前端按 type 渲染，见 MINE_PORTAL_DESIGN.md §7.5）：
//
//	{"type":"stat|list|progress|text|link", "title":"...", "items":[...], ...}
type CardFunc func(ctx context.Context, u *storage.User) (any, error)

// App 是门户里的一个应用/卡片。新增功能只在这里注册，不改门户骨架。
type App struct {
	ID        string
	Name      string
	Desc      string
	Icon      string // 前端图标名（emoji 或内置 svg 名）
	Path      string // 点击跳转的 URL；空 = 纯信息卡
	Order     int
	AdminOnly bool
	Enabled   bool
	Card      CardFunc // 空 = 不展示卡片（只出现在导航）

	// 展示元数据：决定首页怎么摆、顶栏是否出现（见 MINE_PORTAL_DESIGN.md §7.5）。
	// HomeRole: hero（一级入口）/ status（状态）/ content（内容区）/ feed（动态流）/ hidden（不上面）。
	Nav      bool
	NavLabel string // 顶栏显示名（默认用 Name）
	HomeRole string
	Priority int // 越小越靠前
	Span     int // 首页网格跨列（1/2，默认 1）
}

// Register 注册应用（按 ID 去重，后注册者覆盖）。
func (s *Server) Register(apps ...App) {
	for _, a := range apps {
		found := false
		for i := range s.apps {
			if s.apps[i].ID == a.ID {
				s.apps[i] = a
				found = true
				break
			}
		}
		if !found {
			s.apps = append(s.apps, a)
		}
	}
}

// Visible 该用户是否能看到这个应用。
func (s *Server) visible(a App, u *storage.User) bool {
	if !a.Enabled {
		return false
	}
	if a.AdminOnly && (u == nil || !u.IsAdmin) {
		return false
	}
	return true
}

// Apps 返回全部应用（含未启用，前端/调试用）。
func (s *Server) Apps() []App { return s.apps }
