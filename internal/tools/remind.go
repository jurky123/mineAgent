package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"mineagent/internal/storage"
)

// Remind 工具：让 agent 自己安排"到点提醒"。
// 提醒落库（reminders 表），由 internal/reminder 的调度器到点通过
// 当前会话所属通道发一条消息（web/QQ/企微都支持）。
// 限制：每会话最多 5 条待提醒、文本 300 字内、延迟 30 秒 ~ 30 天。
type Remind struct {
	store *storage.Store
	log   *slog.Logger
}

func NewRemind(store *storage.Store, log *slog.Logger) *Remind {
	return &Remind{store: store, log: log}
}

const (
	maxPendingReminders = 5
	maxReminderRunes    = 300
)

func (r *Remind) Tools() []tool.BaseTool {
	return []tool.BaseTool{&remindTool{store: r.store, log: r.log}}
}

type remindTool struct {
	store *storage.Store
	log   *slog.Logger
}

func (t *remindTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "remind",
		Desc: "设置/查看/取消定时提醒。action=add 需要 text + delay_seconds（或 at）；action=list 列出当前会话待提醒；action=cancel 需要 id。到点会通过当前聊天渠道给用户发消息。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"action":        {Type: schema.String, Desc: "add / list / cancel", Required: true},
			"text":          {Type: schema.String, Desc: "提醒内容（add 必填）"},
			"delay_seconds": {Type: schema.Integer, Desc: "多少秒后提醒（add 用，30~2592000）"},
			"at":            {Type: schema.String, Desc: "或指定时间：2006-01-02 15:04 或 RFC3339（与 delay_seconds 二选一）"},
			"id":            {Type: schema.Integer, Desc: "cancel 时必填：提醒编号"},
		}),
	}, nil
}

func (t *remindTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Action       string `json:"action"`
		Text         string `json:"text"`
		DelaySeconds int64  `json:"delay_seconds"`
		At           string `json:"at"`
		ID           int64  `json:"id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return errorJSON("参数不是合法 JSON"), nil
	}
	sessionID := SessionFromContext(ctx)
	if sessionID == "" {
		return errorJSON("取不到当前会话，无法设置提醒"), nil
	}
	now := time.Now()
	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "list":
		rows, err := t.store.ListReminders(ctx, sessionID)
		if err != nil {
			return errorJSON("读取提醒失败：" + err.Error()), nil
		}
		if len(rows) == 0 {
			return "当前没有待提醒。", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "待提醒 %d 条：\n", len(rows))
		for _, r := range rows {
			fmt.Fprintf(&b, "#%d %s（%s后）%s\n", r.ID, time.UnixMilli(r.DueAt).Format("01-02 15:04"), humanUntil(now, r.DueAt), truncate(r.Text, 80))
		}
		return strings.TrimSpace(b.String()), nil
	case "cancel":
		if args.ID <= 0 {
			return errorJSON("cancel 需要 id（可用 action=list 查）"), nil
		}
		ok, err := t.store.CancelReminder(ctx, sessionID, args.ID)
		if err != nil {
			return errorJSON("取消失败：" + err.Error()), nil
		}
		if !ok {
			return errorJSON(fmt.Sprintf("没有找到 #%d 的待提醒", args.ID)), nil
		}
		return fmt.Sprintf("已取消提醒 #%d", args.ID), nil
	case "add":
		text := strings.TrimSpace(args.Text)
		if text == "" {
			return errorJSON("text 不能为空"), nil
		}
		if n := len([]rune(text)); n > maxReminderRunes {
			text = string([]rune(text)[:maxReminderRunes])
		}
		due, err := resolveDue(now, args.DelaySeconds, args.At)
		if err != nil {
			return errorJSON(err.Error()), nil
		}
		if n, err := t.store.CountPendingReminders(ctx, sessionID); err != nil {
			return errorJSON("读取提醒失败：" + err.Error()), nil
		} else if n >= maxPendingReminders {
			return errorJSON(fmt.Sprintf("待提醒已满（%d 条），先取消几条再设", maxPendingReminders)), nil
		}
		rec := storage.Reminder{
			SessionID: sessionID,
			Channel:   channelOfSession(sessionID),
			Target:    QQReplyTargetFromContext(ctx),
			Text:      text,
			DueAt:     due.UnixMilli(),
			CreatedAt: now.UnixMilli(),
		}
		id, err := t.store.AddReminder(ctx, rec)
		if err != nil {
			return errorJSON("保存提醒失败：" + err.Error()), nil
		}
		t.log.Info("reminder added", "session", sessionID, "id", id, "due", due.Format(time.RFC3339), "text", truncate(text, 60))
		return fmt.Sprintf("已设提醒 #%d：%s（%s，%s后我会在这里提醒你）",
			id, truncate(text, 100), due.Format("01-02 15:04"), humanUntil(now, rec.DueAt)), nil
	default:
		return errorJSON("action 只能是 add / list / cancel"), nil
	}
}

func resolveDue(now time.Time, delaySec int64, at string) (time.Time, error) {
	if at = strings.TrimSpace(at); at != "" {
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "15:04"} {
			if t, err := time.ParseInLocation(layout, at, time.Local); err == nil {
				due := t
				if layout == "15:04" { // 只给时间：取今天，已过则明天
					due = time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
					if due.Before(now) {
						due = due.Add(24 * time.Hour)
					}
				}
				if due.Before(now.Add(20 * time.Second)) {
					return time.Time{}, fmt.Errorf("提醒时间要在 20 秒之后")
				}
				if due.After(now.Add(30 * 24 * time.Hour)) {
					return time.Time{}, fmt.Errorf("提醒时间最多 30 天内")
				}
				return due, nil
			}
		}
		return time.Time{}, fmt.Errorf("at 时间格式不对（用 2006-01-02 15:04 或 RFC3339）")
	}
	if delaySec < 30 {
		return time.Time{}, fmt.Errorf("delay_seconds 至少 30 秒")
	}
	if delaySec > 30*24*3600 {
		return time.Time{}, fmt.Errorf("delay_seconds 最多 30 天")
	}
	return now.Add(time.Duration(delaySec) * time.Second), nil
}

func humanUntil(now time.Time, dueMs int64) string {
	d := time.UnixMilli(dueMs).Sub(now)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天", int(d.Hours()/24))
	}
}

// channelOfSession 从会话 key 前缀推断通道（web:xxx / qq:xxx / wecom:xxx / aibot:xxx / wechat:xxx）。
func channelOfSession(sessionID string) string {
	if i := strings.Index(sessionID, ":"); i > 0 {
		return sessionID[:i]
	}
	return "minecraft"
}
