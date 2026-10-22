package qq

import (
	"context"
	"strings"
	"time"

	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

func (c *Channel) conversationCommand(ctx context.Context, scope, target, cmd, arg string, current storage.ChatConversation) {
	var next storage.ChatConversation
	var err error
	var reply string
	switch cmd {
	case "effort":
		fallback := c.cfg.ReasoningEffort
		if fallback == "" {
			fallback = "high"
		}
		reply, err = tools.EffortCommand(ctx, c.store, current.SessionID, arg, fallback)

	case "new", "fork":
		if arg != "" {
			reply = "用法：/" + cmd + "（不带参数）"
			break
		}
		next, err = c.store.NewQQConversation(ctx, scope, cmd == "fork", time.Now().UnixMilli())
		if err == nil {
			reply = "已开启空白对话，旧对话历史已保存。"
			if cmd == "fork" {
				reply = "已复制当前历史并进入独立分支，后续内容分别保存。"
			}
			reply += "\n当前编号：" + next.Conv + "\n/resume 查看并恢复旧对话。原任务若仍在运行会继续留在旧对话，可用 /stop 停止。"
			current = next
		}
	case "resume":
		var offset int
		var list bool
		offset, list, err = tools.ConversationPage(arg)
		if err != nil {
			reply = err.Error()
			err = nil
			break
		}
		if list {
			var entries []storage.ChatConversation
			entries, err = c.store.ListQQConversations(ctx, scope, offset)
			if err == nil {
				reply = tools.ConversationList(current.Conv, entries, offset)
			}
			break
		}
		next, err = c.store.ResumeQQConversation(ctx, scope, arg, time.Now().UnixMilli())
		if err == nil {
			current = next
			reply = "已恢复对话：" + next.Conv + "。接下来的消息将在该对话中继续。"
		}
	}
	if err != nil {
		reply = "会话设置或切换失败，本次未生效。请稍后重试。"
		if err == storage.ErrConversationNotFound {
			reply = err.Error() + "。用 /resume 查看当前范围的会话。"
		}
	}
	if strings.HasPrefix(scope, "qq:group:") {
		reply += "\n这是当前群的共享对话。"
	}
	sess, e := c.session(current.SessionID)
	if e == nil {
		_ = sess.Notice(ctx, reply, target)
	}
}
