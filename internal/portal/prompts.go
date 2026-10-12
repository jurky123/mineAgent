package portal

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"mineagent/internal/prompts"
	"mineagent/internal/storage"
)

// handlePrompts 系统提示词：
//   - GET：列出各服务（可选带内置默认与全局覆盖，仅管理员可见）
//   - POST：scope=global 需管理员（完全替换默认）；scope=user 是网页追加（仅本人）
func (s *Server) handlePrompts(w http.ResponseWriter, r *http.Request, u *storage.User) {
	switch r.Method {
	case http.MethodGet:
		services := make([]map[string]any, 0)
		for _, svc := range prompts.Services() {
			item := map[string]any{"id": svc.ID, "name": svc.Name, "desc": svc.Desc}
			if u.IsAdmin {
				item["default"] = prompts.DefaultText(svc.ID)
				item["global"] = prompts.Global(svc.ID)
			}
			services = append(services, item)
		}
		user := map[string]string{}
		if v := prompts.UserAppend("web", u.Username); v != "" {
			user["web"] = v
		}
		writeJSON(w, http.StatusOK, map[string]any{"services": services, "admin": u.IsAdmin, "user": user})
	case http.MethodPost:
		var req struct {
			Scope   string  `json:"scope"`
			ID      string  `json:"id"`
			Enabled *bool   `json:"enabled"`
			Text    *string `json:"text"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "参数不是合法 JSON"})
			return
		}
		id := strings.TrimSpace(req.ID)
		text := ""
		if req.Text != nil {
			text = *req.Text
		}
		switch strings.TrimSpace(req.Scope) {
		case "user":
			if id != "web" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "目前只支持追加网页通道提示词"})
				return
			}
			if err := prompts.SetUserAppend(id, u.Username, text); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		case "global":
			if !u.IsAdmin {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "只有管理员能改全局提示词"})
				return
			}
			// 文本为空且没带 enabled：视为"恢复默认"
			if strings.TrimSpace(text) == "" && req.Enabled == nil {
				if err := prompts.ResetGlobal(id); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
					return
				}
				break
			}
			enabled := true
			if req.Enabled != nil {
				enabled = *req.Enabled
			}
			if err := prompts.SetGlobal(id, prompts.Override{Enabled: enabled, Text: text}); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope 只能是 global/user"})
			return
		}
		s.log.Info("prompts updated", "by", u.Username, "scope", req.Scope, "service", id)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 GET/POST"})
	}
}
