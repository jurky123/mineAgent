package qq

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"mineagent/internal/prompts"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

// WithModelCatalog uses the same selectable IDs as the website, without sharing
// account preferences or credentials. Protected because QQ starts before web.
func (c *Channel) WithModelCatalog(fn func(context.Context) []string) *Channel {
	c.mu.Lock()
	c.modelCatalog = fn
	c.mu.Unlock()
	return c
}

func matchModelCommand(text string) (string, string) {
	f := strings.Fields(text)
	if len(f) == 0 {
		return "", ""
	}
	switch strings.ToLower(f[0]) {
	case "/model", "/settings", "/设置":
		return strings.TrimPrefix(strings.ToLower(f[0]), "/"), strings.Join(f[1:], " ")
	}
	return "", ""
}

func (c *Channel) selectableModels(ctx context.Context) []string {
	c.mu.Lock()
	fn := c.modelCatalog
	ids := append([]string(nil), c.configuredModels...)
	c.mu.Unlock()
	if fn != nil {
		limited, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		ids = fn(limited)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}

func (c *Channel) modelCommand(ctx context.Context, scope, target, cmd, arg string, current storage.ChatConversation) {
	reply, err := c.modelSetting(ctx, current.SessionID, cmd, arg)
	if err != nil {
		reply = "读取或保存模型设置失败，本次未生效。请稍后重试。"
	}
	if strings.HasPrefix(scope, "qq:group:") {
		reply += "\n设置作用于当前群的共享对话；不影响其他群或私聊。"
	}
	if sess, err := c.session(current.SessionID); err == nil {
		_ = sess.Notice(ctx, reply, target)
	}
}

func (c *Channel) modelSetting(ctx context.Context, key, cmd, arg string) (string, error) {
	selected, err := c.store.SessionModel(ctx, key)
	if err != nil {
		return "", err
	}
	label := selected
	if label == "" {
		label = "默认（" + c.defaultModel() + "）"
	}
	if cmd == "settings" || cmd == "设置" {
		if arg != "" {
			return "用法：/settings（或 /设置）查看会话设置。", nil
		}
		effort, err := tools.EffortCommand(ctx, c.store, key, "", firstNonEmpty(c.cfg.ReasoningEffort, "high"))
		return "当前会话设置\n模型：" + label + "\n" + effort + "\n/model list 查看模型\n/model <模型ID> 切换；/model default 恢复默认\n仅影响后续新请求，已提交的任务不变。", err
	}
	if arg == "" {
		return "当前会话模型：" + label + "\n/model list 查看可选模型；/model list <页码> 翻页\n/model <模型ID> 切换；/model default 恢复默认\n/settings 查看全部会话设置。", nil
	}
	f := strings.Fields(arg)
	if strings.EqualFold(f[0], "list") {
		page := 1
		if len(f) == 2 {
			page, err = strconv.Atoi(f[1])
		}
		if len(f) > 2 || err != nil || page < 1 || page > 100000 {
			return "用法：/model list [页码]，页码为正整数。", nil
		}
		ids := c.selectableModels(ctx)
		if len(ids) == 0 {
			return "暂未取得可选模型，请稍后重试；/model default 可恢复默认。", nil
		}
		start := (page - 1) * 20
		if start >= len(ids) {
			return "这一页没有模型。/model list 返回第一页。", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "可选模型（第 %d/%d 页）\n当前：%s\n", page, (len(ids)+19)/20, label)
		for _, id := range ids[start:min(start+20, len(ids))] {
			b.WriteString("• " + id + "\n")
		}
		b.WriteString("/model <模型ID> 切换；/model default 恢复默认。\n可选不代表提供商保证额度或可用性。")
		return b.String(), nil
	}
	if len(f) != 1 {
		return "用法：/model <完整模型ID>，或 /model list 查看列表。", nil
	}
	value := f[0]
	if strings.EqualFold(value, "default") || value == "默认" {
		value = ""
	} else {
		allowed := false
		for _, id := range c.selectableModels(ctx) {
			if id == value {
				allowed = true
				break
			}
		}
		if !allowed {
			return "该模型不在当前可选列表，设置未更改。请用 /model list 复制完整模型ID。", nil
		}
	}
	if err := c.store.SetSessionModel(ctx, key, value); err != nil {
		return "", err
	}
	if value == "" {
		value = "默认（" + c.defaultModel() + "）"
	}
	return "当前会话模型已设为：" + value + "。\n从下一条新请求开始生效，已提交的任务不变。/settings 查看设置。", nil
}

func (c *Channel) defaultModel() string { return prompts.ModelFor("qq", "", c.model) }
