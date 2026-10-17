package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"mineagent/internal/storage"
)

type capability struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Kind   string `json:"kind"`
}

func toolAccess(name string, admin bool) (string, string) {
	if strings.HasPrefix(name, "workspace_") || name == "web_file" {
		if !admin {
			return "admin", "文件与执行"
		}
		return "available", "文件与执行"
	}
	if name == "minecraft_teleport" || name == "minecraft_give" || name == "minecraft_run_command" {
		return "approval", "修改世界"
	}
	if name == "remind" || strings.Contains(name, "bind") {
		return "available", "创建或修改个人数据"
	}
	return "available", "查询"
}
func (c *Channel) capabilities(ctx context.Context, u *storage.User) []capability {
	out := []capability{}
	for _, t := range c.webTools {
		if info, err := t.Info(ctx); err == nil && info != nil {
			status, kind := toolAccess(info.Name, u.IsAdmin)
			out = append(out, capability{info.Name, status, kind})
		}
	}
	return out
}
func (c *Channel) toolsForUser(ctx context.Context, u *storage.User) []tool.BaseTool {
	out := []tool.BaseTool{}
	for _, t := range c.webTools {
		if info, err := t.Info(ctx); err == nil && info != nil {
			status, _ := toolAccess(info.Name, u.IsAdmin)
			if status != "admin" {
				out = append(out, t)
			}
		}
	}
	return out
}
func (c *Channel) capabilityInstruction(ctx context.Context, u *storage.User) string {
	b, _ := json.Marshal(c.capabilities(ctx, u))
	return "\n本次请求的实际能力清单（服务端生成，不可由人设覆盖）：" + string(b) + "\nstatus=available 可用，approval 必须等待管理员批准，admin 当前用户无权使用。查询与创建提醒/修改数据必须区分；只有真实工具结果才能证明已执行。不要宣称清单之外的能力。"
}
func (c *Channel) skillsForUser(ctx context.Context, u *storage.User) []map[string]any {
	tools := map[string]string{}
	for _, entry := range c.capabilities(ctx, u) {
		tools[entry.Name] = entry.Status
	}
	out := []map[string]any{}
	for _, skill := range c.cfg.Skills {
		needed := skill.Tools
		if len(needed) == 0 {
			switch skill.Name {
			case "画张图", "写代码跑一下":
				needed = []string{"workspace_exec"}
			case "查资料":
				needed = []string{"web_search"}
			case "查服务器":
				needed = []string{"minecraft_server_status"}
			}
		}
		status, reason := "available", "快捷输入，确认后发送"
		for _, name := range needed {
			switch tools[name] {
			case "":
				status = "unavailable"
				reason = "当前未配置所需工具"
			case "admin":
				if status != "unavailable" {
					status = "admin"
					reason = "需要管理员权限，可联系管理员"
				}
			case "approval":
				if status == "available" {
					status = "approval"
					reason = "执行前需要审批"
				}
			}
		}
		desc := skill.Desc
		if skill.Name == "查资料" {
			desc = "联网搜索与读取公开网页"
		}
		out = append(out, map[string]any{"name": skill.Name, "prompt": skill.Prompt, "desc": desc, "status": status, "reason": reason})
	}
	return out
}

// handleStop scopes cancellation to the authenticated user's own conversation.
func (c *Channel) handleStop(w http.ResponseWriter, r *http.Request, u *storage.User) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "只支持 POST"})
		return
	}
	var req struct {
		Conv string `json:"conv"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req) != nil || !ValidConv(req.Conv) {
		writeJSON(w, 400, map[string]string{"error": "会话 id 非法"})
		return
	}
	key := webSessionKey(u.ID, req.Conv)
	c.mu.Lock()
	cancel := c.active[key]
	if cancel != nil {
		st := c.running[key]
		st.Text = "正在停止…"
		c.running[key] = st
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		if c.ag != nil {
			c.ag.CancelPending(key)
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true, "stopping": cancel != nil})
}
func (c *Channel) handleConversationSettings(w http.ResponseWriter, r *http.Request, u *storage.User) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "只支持 POST"})
		return
	}
	var req struct {
		Conv     string `json:"conv"`
		Pinned   bool   `json:"pinned"`
		Archived bool   `json:"archived"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req) != nil || !ValidConv(req.Conv) {
		writeJSON(w, 400, map[string]string{"error": "会话 id 非法"})
		return
	}
	cv, err := c.store.ConversationFor(r.Context(), u.ID, u.Username, req.Conv)
	if err != nil || cv == nil {
		writeJSON(w, 404, map[string]string{"error": "会话不存在"})
		return
	}
	if err = c.store.SetConversationSettings(r.Context(), u.ID, req.Conv, req.Pinned, req.Archived); err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法保存会话设置"})
		return
	}
	c.publishRaw(u.Username, map[string]string{"type": "conversations"})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
