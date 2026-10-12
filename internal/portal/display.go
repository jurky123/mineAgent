package portal

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"mineagent/internal/storage"
)

// displayHTTPClient 转发控制 API 用（本地回环，短超时）。
var displayHTTPClient = &http.Client{Timeout: 8 * time.Second}

// handleDisplayProxy 把 /api/display/* 转发给 MineDisplay 本地控制 API，
// 带上从 token 文件读出的 Bearer 令牌。
// GET 任意登录用户；写操作限管理员（开屏/关屏/改白名单都能影响游戏内）。
func (s *Server) handleDisplayProxy(w http.ResponseWriter, r *http.Request) {
	s.requireUser(func(w http.ResponseWriter, r *http.Request, u *storage.User) {
		if r.Method != http.MethodGet && (u == nil || !u.IsAdmin) {
			writeJSONError(w, http.StatusForbidden, "需要管理员")
			return
		}
		base := strings.TrimRight(s.cfg.DisplayAPI, "/")
		if base == "" {
			writeJSONError(w, http.StatusServiceUnavailable, "未配置 MineDisplay 控制地址")
			return
		}
		token := readTokenFile(s.cfg.DisplayTokenFile)
		if token == "" {
			writeJSONError(w, http.StatusServiceUnavailable, "MineDisplay 控制 API 未就绪（找不到 token 文件）")
			return
		}
		target := base + strings.TrimPrefix(r.URL.Path, "/api/display")
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := displayHTTPClient.Do(req)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "控制 API 不可达: "+err.Error())
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})(w, r)
}

// readTokenFile 读控制 API 的 Bearer 令牌文件（MineDisplay 自动生成）。
func readTokenFile(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]string{"error": message})
	_, _ = w.Write(b)
}
