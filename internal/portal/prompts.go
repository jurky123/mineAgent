package portal

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"mineagent/internal/prompts"
	"mineagent/internal/storage"
)

// handlePrompts 人设 + 系统提示词：
//   - GET：服务列表（含内置系统提示词与覆盖，仅管理员可见细节）、人设模板、
//     各服务的全局人设、当前用户自己的网页人设
//   - POST scope=capability（系统提示词，管理员）/ service-persona（按服务选人设，管理员）/
//     user-persona（本人网页人设）/ persona-save / persona-delete（人设模板，管理员）
func (s *Server) handlePrompts(w http.ResponseWriter, r *http.Request, u *storage.User) {
	switch r.Method {
	case http.MethodGet:
		s.promptsGet(w, u)
	case http.MethodPost:
		s.promptsPost(w, r, u)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "只支持 GET/POST"})
	}
}

func (s *Server) promptsGet(w http.ResponseWriter, u *storage.User) {
	services := make([]map[string]any, 0)
	for _, svc := range prompts.Services() {
		item := map[string]any{"id": svc.ID, "name": svc.Name, "desc": svc.Desc,
			"persona": prompts.ServicePersona(svc.ID), "defaultPersona": prompts.DefaultPersonaFor(svc.ID)}
		if u.IsAdmin {
			item["default"] = prompts.DefaultText(svc.ID)
			item["global"] = prompts.Global(svc.ID)
		}
		services = append(services, item)
	}
	personas := make([]map[string]any, 0)
	for _, p := range prompts.Personas() {
		// 人设是共享模板，登录用户可预览正文；渠道系统提示词细节仍只对管理员开放。
		item := map[string]any{"id": p.ID, "name": p.Name, "desc": p.Desc, "builtin": p.Builtin, "text": p.Text, "example": p.Example}
		personas = append(personas, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"admin":      u.IsAdmin,
		"services":   services,
		"personas":   personas,
		"myPersona":  prompts.UserPersona(u.Username),
		"style":      prompts.UserPreferences(u.Username),
		"webPersona": prompts.ServicePersona("web"),
		"limits": map[string]int{"persona": prompts.MaxPersonaLen, "capability": prompts.MaxCapabilityLen,
			"name": prompts.MaxNameLen, "desc": prompts.MaxDescLen},
	})
}

func (s *Server) promptsPost(w http.ResponseWriter, r *http.Request, u *storage.User) {
	var req struct {
		Scope   string             `json:"scope"`
		ID      string             `json:"id"`
		Enabled *bool              `json:"enabled"`
		Text    *string            `json:"text"`
		Persona string             `json:"persona"`
		Data    json.RawMessage    `json:"data"`
		Style   *prompts.UserStyle `json:"style"`
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
	case "user-settings":
		if req.Style == nil {
			writeJSON(w, 400, map[string]string{"error": "请提交完整的个人偏好"})
			return
		}
		if err := prompts.SetUserSettings(u.Username, strings.TrimSpace(req.Persona), *req.Style); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
	case "user-persona":
		if err := prompts.SetUserPersona(u.Username, strings.TrimSpace(req.Persona)); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	case "capability", "service-persona", "service-settings", "persona-save", "persona-delete":
		if !u.IsAdmin {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "只有管理员能改"})
			return
		}
		switch strings.TrimSpace(req.Scope) {
		case "service-settings":
			if req.Enabled == nil || req.Text == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "渠道设置需要完整的 enabled 与 text"})
				return
			}
			enabled := req.Enabled != nil && *req.Enabled
			if err := prompts.SetServiceSettings(id, prompts.CapabilityOverride{Enabled: enabled, Text: text}, strings.TrimSpace(req.Persona)); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		case "capability":
			// 文本为空且没带 enabled：视为"恢复内置系统提示词"
			if strings.TrimSpace(text) == "" && req.Enabled == nil {
				if err := prompts.ResetCapabilities(id); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
					return
				}
				break
			}
			enabled := true
			if req.Enabled != nil {
				enabled = *req.Enabled
			}
			if err := prompts.SetCapabilities(id, prompts.CapabilityOverride{Enabled: enabled, Text: text}); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		case "service-persona":
			if err := prompts.SetServicePersona(id, strings.TrimSpace(req.Persona)); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		case "persona-save":
			var p prompts.Persona
			if len(req.Data) > 0 {
				if err := json.Unmarshal(req.Data, &p); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "人设数据不是合法 JSON"})
					return
				}
			} else {
				p = prompts.Persona{ID: id, Text: text}
			}
			saved, err := prompts.SavePersona(p)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			id = saved.ID
		case "persona-delete":
			if err := prompts.DeletePersona(id); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "未知 scope"})
		return
	}
	s.log.Info("prompts updated", "by", u.Username, "scope", req.Scope, "id", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}
