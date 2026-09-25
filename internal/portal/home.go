package portal

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"mineagent/internal/storage"
	"mineagent/internal/webui"
)

// handleApps 应用注册表：导航与卡片的数据源（公开，便于未登录展示）。
func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	type appWire struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Desc      string `json:"desc,omitempty"`
		Icon      string `json:"icon,omitempty"`
		Path      string `json:"path,omitempty"`
		Order     int    `json:"order"`
		AdminOnly bool   `json:"adminOnly,omitempty"`
		Enabled   bool   `json:"enabled"`
		Nav       bool   `json:"nav,omitempty"`
		NavLabel  string `json:"navLabel,omitempty"`
		HomeRole  string `json:"homeRole,omitempty"`
	}
	out := make([]appWire, 0, len(s.apps))
	for _, a := range s.apps {
		out = append(out, appWire{a.ID, a.Name, a.Desc, a.Icon, a.Path, a.Order, a.AdminOnly, a.Enabled, a.Nav, a.NavLabel, a.HomeRole})
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": out, "portal": s.PortalEnabled()})
}

// handleHome 首页聚合：欢迎语 + 各应用卡片。单个应用失败只降级该卡片。
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request, u *storage.User) {
	type appCard struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Path     string `json:"path,omitempty"`
		Role     string `json:"role"`
		Priority int    `json:"priority"`
		Span     int    `json:"span,omitempty"`
		Card     any    `json:"card,omitempty"`
		Error    string `json:"error,omitempty"`
	}
	apps := make([]appCard, 0, len(s.apps))
	sorted := append([]App(nil), s.apps...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })
	for _, a := range sorted {
		if a.AdminOnly && (u == nil || !u.IsAdmin) {
			continue
		}
		if !a.Enabled {
			continue // 开发中的功能不出现在首页（避免"占位卡"）
		}
		item := appCard{ID: a.ID, Name: a.Name, Path: a.Path, Role: a.HomeRole, Priority: a.Priority, Span: a.Span}
		if a.Card != nil {
			ctx, cancel := withTimeout(r.Context(), 3*time.Second)
			card, err := a.Card(ctx, u)
			cancel()
			if err != nil {
				s.log.Warn("portal card failed", "app", a.ID, "user", u.Username, "err", err)
				item.Error = "暂不可用"
			} else {
				item.Card = card
			}
		}
		apps = append(apps, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"greeting": greeting(u.DisplayName),
		"apps":     apps,
		"account": map[string]any{
			"id":    u.ID,
			"name":  u.Username,
			"admin": u.IsAdmin,
		},
	})
}

func greeting(name string) string {
	h := time.Now().Hour()
	switch {
	case h < 6:
		return "夜深了，" + name
	case h < 12:
		return "早上好，" + name
	case h < 14:
		return "中午好，" + name
	case h < 18:
		return "下午好，" + name
	default:
		return "晚上好，" + name
	}
}

// handleAccountStats 账号页的"游戏"分区：按游戏聚合的战绩（没有记录就没有数据）。
func (s *Server) handleAccountStats(w http.ResponseWriter, r *http.Request, u *storage.User) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	stats, err := s.store.GameStats(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if stats == nil {
		stats = []storage.GameStat{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"games": stats})
}

// handleAnnouncements 公告栏：GET 公开（未登录也能看），POST/DELETE 仅管理员。
func (s *Server) handleAnnouncements(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		list, err := s.store.ListAnnouncements(r.Context(), limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"announcements": list})
	case http.MethodPost:
		s.requireAdmin(w, r, func(w http.ResponseWriter, r *http.Request, u *storage.User) {
			var req struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "参数不是合法 JSON"})
				return
			}
			text := strings.TrimSpace(req.Text)
			if text == "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "公告内容不能为空"})
				return
			}
			if r := []rune(text); len(r) > 500 {
				text = string(r[:500])
			}
			a, err := s.store.AddAnnouncement(r.Context(), text, u.Username, time.Now().UnixMilli())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			s.log.Info("announcement posted", "user", u.Username, "id", a.ID)
			writeJSON(w, http.StatusOK, a)
		})
	case http.MethodDelete:
		s.requireAdmin(w, r, func(w http.ResponseWriter, r *http.Request, u *storage.User) {
			id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
			if id <= 0 {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id 非法"})
				return
			}
			ok, err := s.store.DeleteAnnouncement(r.Context(), id)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			if !ok {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "公告不存在"})
				return
			}
			s.log.Info("announcement deleted", "user", u.Username, "id", id)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET/POST/DELETE"})
	}
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request, next func(w http.ResponseWriter, r *http.Request, u *storage.User)) {
	s.requireUser(func(w http.ResponseWriter, r *http.Request, u *storage.User) {
		if !u.IsAdmin {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "需要管理员权限（web.adminUsers）"})
			return
		}
		next(w, r, u)
	})(w, r)
}

// handleSessions 已登录设备列表（当前设备标 current）。
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request, u *storage.User) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	_, currentID, _ := s.acct.CurrentWithSession(r.Context(), webui.TokenOf(r))
	list, err := s.store.ListAuthSessions(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	type item struct {
		ID         int64  `json:"id"`
		UserAgent  string `json:"userAgent"`
		IP         string `json:"ip"`
		CreatedAt  int64  `json:"createdAt"`
		LastSeenAt int64  `json:"lastSeenAt"`
		Current    bool   `json:"current"`
	}
	out := make([]item, 0, len(list))
	for _, a := range list {
		out = append(out, item{a.ID, shortUA(a.UserAgent), a.IP, a.CreatedAt, a.LastSeenAt, a.ID == currentID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// handleSessionsRevoke 注销设备：{"id":N} 或 {"others":true}。
func (s *Server) handleSessionsRevoke(w http.ResponseWriter, r *http.Request, u *storage.User) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 POST"})
		return
	}
	var req struct {
		ID     int64 `json:"id"`
		Others bool  `json:"others"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "参数不是合法 JSON"})
		return
	}
	if req.Others {
		_, currentID, _ := s.acct.CurrentWithSession(r.Context(), webui.TokenOf(r))
		if _, err := s.store.DeleteOtherAuthSessions(r.Context(), u.ID, currentID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if req.ID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id 非法"})
		return
	}
	ok, err := s.store.DeleteAuthSessionByIDForUser(r.Context(), req.ID, u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok})
}

// shortUA 设备列表展示用：取浏览器与系统的大致信息，避免超长。
func shortUA(ua string) string {
	ua = strings.TrimSpace(ua)
	if len(ua) > 120 {
		ua = ua[:120]
	}
	return ua
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
