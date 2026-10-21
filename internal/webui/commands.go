package webui

import (
	"context"
	"strings"
	"time"

	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

// Direct commands run before the model queue, run lock and chat rate limiter.
func (c *Channel) conversationCommand(ctx context.Context, u *storage.User, conv *string, cmd, arg string) (string, error) {
	current := ""
	if conv != nil {
		current = *conv
	}
	if conv != nil && current != "" {
		found, err := c.store.ConversationFor(ctx, u.ID, u.Username, current)
		if err != nil {
			return current, err
		}
		if found == nil {
			return current, ErrConvNotFound
		}
	}
	now := time.Now().UnixMilli()
	// Keep pre-conversation-table default history addressable without changing ids.
	if n, err := c.store.CountMessages(ctx, webSessionKey(u.ID, "")); err != nil {
		return current, err
	} else if n > 0 || (conv != nil && current == "") {
		if err = c.store.UpsertConversationFor(ctx, u.ID, u.Username, "", "", now); err != nil {
			return current, err
		}
	}
	selected := current
	haveSelection := conv != nil
	reply := ""
	switch cmd {
	case "help":
		reply = tools.ExecSystemCommand(tools.SysCtx{Ctx: ctx, Store: c.store, SessionID: webSessionKey(u.ID, current), Channel: "web", IsAdmin: u.IsAdmin}, "help", "")
	case "new", "fork":
		if arg != "" {
			reply = "用法：/" + cmd + "（不带参数）"
			break
		}
		id, err := storage.NewConversationID()
		if err != nil {
			return current, err
		}
		title := "新对话"
		var source *string
		if cmd == "fork" {
			title = "对话分支"
			if conv != nil {
				original, e := c.store.ConversationFor(ctx, u.ID, u.Username, *conv)
				if e != nil {
					return current, e
				}
				if original != nil && original.Title != "" {
					name := []rune(original.Title)
					title = string(name[:min(len(name), 48)]) + " · 分支"
				}
			}
			source = conv
		}
		if err = c.store.CreateWebConversation(ctx, u.ID, u.Username, id, title, source, now); err != nil {
			return current, err
		}
		selected = id
		haveSelection = true
		reply = "已开启空白对话，旧对话历史已保存。"
		if cmd == "fork" {
			reply = "已复制当前已保存的历史并进入独立分支；原对话后续回复不会同步到分支。"
			if current != "" {
				reply += "\n来源会话：" + current
			}
		}
		reply += "\n当前编号：" + id + "\n/resume 查看并恢复旧对话。旧对话中的任务会继续，可切回原对话停止。"
	case "resume":
		offset, list, err := tools.ConversationPage(arg)
		if err != nil {
			reply = err.Error()
			break
		}
		if list {
			rows, err := c.store.ListConversationsByUser(ctx, u.ID, u.Username)
			if err != nil {
				return current, err
			}
			entries := []storage.ChatConversation{}
			for _, row := range rows[min(offset, len(rows)):min(offset+21, len(rows))] {
				id := row.Conv
				if id == "" {
					id = "default"
				}
				entries = append(entries, storage.ChatConversation{Conv: id, Title: row.Title, UpdatedAt: row.UpdatedAt})
			}
			id := current
			if id == "" && haveSelection {
				id = "default"
			}
			reply = tools.ConversationList(id, entries, offset)
			break
		}
		id := arg
		if id == "default" {
			id = ""
		}
		row, err := c.store.ConversationFor(ctx, u.ID, u.Username, id)
		if err != nil {
			return current, err
		}
		if row == nil {
			reply = "会话不存在或不属于当前账号。请用 /resume 查看可恢复的会话。"
			break
		}
		selected = id
		haveSelection = true
		reply = "已恢复对话：" + arg + "。接下来的消息将在该对话继续。"
		if settings, err := c.store.ConversationSettings(ctx, u.ID); err == nil && settings[id].Archived {
			if err = c.store.SetConversationSettings(ctx, u.ID, id, settings[id].Pinned, false); err != nil {
				return current, err
			}
		}
	}
	if !haveSelection {
		id, err := storage.NewConversationID()
		if err != nil {
			return current, err
		}
		if err = c.store.CreateWebConversation(ctx, u.ID, u.Username, id, "会话命令", nil, now); err != nil {
			return current, err
		}
		selected = id
	}
	sess, err := c.session(webSessionKey(u.ID, selected))
	if err != nil {
		return selected, err
	}
	err = sess.Notice(ctx, reply, "c2c:"+u.Username)
	c.publishRaw(u.Username, map[string]any{"type": "conversations"})
	return selected, err
}

func directConversationCommand(text string) (string, string) {
	if cmd, arg := tools.MatchConversationCommand(text); cmd != "" {
		return cmd, arg
	}
	if cmd, _ := tools.MatchSystemCommand(strings.TrimSpace(text)); cmd == "help" {
		return "help", ""
	}
	return "", ""
}
