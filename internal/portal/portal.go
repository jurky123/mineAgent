// Package portal 是站点外壳：页面路由 + 应用注册表 + 首页聚合 + 账号 API。
//
// 与 webui 的关系：webui 仍持有 Agent（聊天）通道与它的 /api/*、
// 静态资源、HTTP Server；portal 负责把页面与 API 组合成站点。
// 新增功能一律"注册一个 App"，不改这里（见 MINE_PORTAL_DESIGN.md §7.5）。
package portal

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"mineagent/internal/account"
	"mineagent/internal/config"
	"mineagent/internal/games"
	"mineagent/internal/storage"
	"mineagent/internal/webui"

	"golang.org/x/crypto/acme/autocert"
)

type Server struct {
	log   *slog.Logger
	cfg   config.Web
	store *storage.Store
	acct  *account.Service
	web   *webui.Channel
	apps  []App
	games http.Handler // 小游戏 API（main 注入，见 WithGames）

	acme      *autocert.Manager // 启用 HTTPS 时才有
	tlsDomain string
	tlsSrv    *http.Server
}

func New(log *slog.Logger, cfg config.Config, store *storage.Store, acct *account.Service, web *webui.Channel) *Server {
	return &Server{log: log, cfg: cfg.Web, store: store, acct: acct, web: web}
}

// PortalEnabled 门户是否启用（默认启用；web.portal=false 回滚为旧的单页模式）。
func (s *Server) PortalEnabled() bool {
	return s.cfg.Portal == nil || *s.cfg.Portal
}

// WithGames 挂上小游戏 API（internal/games 的 Handler）。
func (s *Server) WithGames(h http.Handler) *Server {
	s.games = h
	return s
}

// Auth 把门户的鉴权中间件暴露给 games 包（它要包自己的路由）。
func (s *Server) Auth() games.Authed { return s.requireUser }

// Start 起 HTTP 服务（阻塞，调用方 go）。
func (s *Server) Start() error {
	return s.web.ServeOn(s.cfg.Listen, s.Handler())
}

func (s *Server) Stop() {
	s.web.Stop()
	if s.tlsSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.tlsSrv.Shutdown(ctx)
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	api := s.web.API()

	// 账号 API（新路径；/api/login 等旧路径由 webui.API 继续提供）
	mux.HandleFunc("/api/auth/login", s.web.HandleLogin)
	mux.HandleFunc("/api/auth/logout", s.web.HandleLogout)
	mux.HandleFunc("/api/auth/me", s.web.HandleMe)
	mux.HandleFunc("/api/account/sessions", s.requireUser(s.handleSessions))
	mux.HandleFunc("/api/account/stats", s.requireUser(s.handleAccountStats))
	mux.HandleFunc("/api/account/sessions/revoke", s.requireUser(s.handleSessionsRevoke))

	// Portal API
	mux.HandleFunc("/api/portal/apps", s.handleApps)
	mux.HandleFunc("/api/portal/home", s.requireUser(s.handleHome))
	mux.HandleFunc("/api/portal/presence", s.requireUser(s.handlePresence))
	mux.HandleFunc("/api/portal/announcements", s.handleAnnouncements) // GET 公开；POST/DELETE 管理员

	if s.games != nil {
		mux.Handle("/api/games", s.games)
		mux.Handle("/api/games/", s.games)
	}
	// PWA：manifest 与 Service Worker（注入版本号；纯网络 SW，不做离线缓存）
	mux.HandleFunc("/manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		b, err := webui.RenderPage("manifest.webmanifest")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		b, err := webui.RenderPage("sw.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Service-Worker-Allowed", "/")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		b, err := webui.RenderPage("img/favicon.png")
		if err != nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	mux.Handle("/api/", api)
	mux.Handle("/static/", api)
	mux.HandleFunc("/", s.handlePage)
	return s.redirectHTTPS(mux)
}

// redirectHTTPS 把走 http 的域名页面请求 301 到 https（启用 web.domain 后生效）。
// 例外：非 GET/HEAD（走 API 的 POST 不能被改语义）、/api/、/.well-known/（ACME）、
// 以及 Host 不是本站域名的请求（比如直接访问 IP:8766）。
func (s *Server) redirectHTTPS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.tlsDomain == "" || r.TLS != nil {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/.well-known/") {
			next.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host[i:], "]") {
			host = host[:i]
		}
		if !strings.EqualFold(host, s.tlsDomain) {
			next.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, "https://"+s.tlsDomain+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}

// 页面白名单：URL -> 内嵌文件（相对 internal/webui/static）。
var pages = map[string]string{
	"/":                "portal/index.html",
	"/agent":           "chat/index.html",
	"/account":         "account/index.html",
	"/account/history": "account/history/index.html",
	"/games":           "games/index.html",
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	p := r.URL.Path
	if !s.PortalEnabled() {
		// 回滚模式：/ 是聊天页，其它门户页 302 回 /。
		if p == "/" {
			s.render(w, "chat/index.html")
			return
		}
		if p == "/agent" || p == "/account" || strings.HasPrefix(p, "/games") {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
		return
	}
	if p == "/agent" {
		s.render(w, "chat/index.html")
		return
	}
	if file, ok := pages[p]; ok {
		s.render(w, file)
		return
	}
	if id, ok := strings.CutPrefix(p, "/games/"); ok && id != "" && s.gamePageExists(id) {
		s.render(w, "games/"+id+"/index.html")
		return
	}
	http.NotFound(w, r)
}

// gamePageExists 游戏页是否存在（注册表启用 + 内嵌页存在）。
func (s *Server) gamePageExists(id string) bool {
	if !games.Enabled(id) {
		return false
	}
	_, err := webui.RenderPage("games/" + id + "/index.html")
	return err == nil
}

// render 渲染内嵌页面（注入版本号，禁缓存）。
func (s *Server) render(w http.ResponseWriter, name string) {
	b, err := webui.RenderPage(name)
	if err != nil {
		http.NotFound(w, nil) //nolint:staticcheck
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

// requireUser 校验登录（Bearer / ?token= / cookie），把用户放进 context。
func (s *Server) requireUser(next func(w http.ResponseWriter, r *http.Request, u *storage.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := webui.TokenOf(r)
		u := s.acct.Current(r.Context(), tok)
		if u == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "未登录或登录已过期"})
			return
		}
		webui.SetAuthCookie(w, r, tok)
		next(w, r, u)
	}
}

// withTimeout 给应用卡片一个短超时：单个应用挂掉不拖垮整页。
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}
