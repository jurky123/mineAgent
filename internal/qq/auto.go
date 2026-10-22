package qq

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mineagent/internal/agent"
	"mineagent/internal/prompts"
	"mineagent/internal/storage"
)

const (
	autoQuiet          = 5 * time.Second
	autoCooldown       = 2 * time.Minute
	autoHourlyAttempts = 6
)

type autoGroup struct {
	generation            uint64
	timer                 *time.Timer
	cancel                context.CancelFunc
	messages              int
	lastSeen, lastAttempt time.Time
	attempts              []time.Time
}

func (g *autoGroup) cancelPending() {
	g.generation++
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
}

// All methods here are called with routingMu held. State is scoped by platform
// group, never by nickname. The switch persists across /new and service restarts.
func (c *Channel) autoState(scope string) *autoGroup {
	if c.autoGroups == nil {
		c.autoGroups = make(map[string]*autoGroup)
	}
	if c.autoGroups[scope] == nil {
		c.autoGroups[scope] = &autoGroup{}
	}
	return c.autoGroups[scope]
}

func (c *Channel) autoEnabled(ctx context.Context, scope string) bool {
	value, _, err := c.store.GetMeta(ctx, "qq:auto:"+scope)
	return err == nil && value == "on"
}

func (c *Channel) autoCommand(ctx context.Context, m InboundMessage, scope, sessionKey, target, text string) bool {
	args := strings.Fields(text)
	if len(args) == 0 || strings.ToLower(args[0]) != "/auto" {
		return false
	}
	sess, err := c.session(sessionKey)
	if err != nil {
		return true
	}
	reply := "/auto 查看状态；/auto on 开启本群自然接话；/auto off 关闭。仅配置的 QQ 管理员可以修改。"
	if !strings.HasPrefix(scope, "qq:group:") {
		reply = "自然接话按群开启，请在目标群使用 /auto。私聊消息本来就会回复。"
	} else if len(args) == 1 || (len(args) == 2 && args[1] == "status") {
		status := "关闭"
		if c.autoEnabled(ctx, scope) {
			status = "开启"
		}
		received := "本次启动后尚未收到普通群消息"
		if seen := c.autoState(scope).lastSeen; !seen.IsZero() {
			received = "最近收到普通群消息：" + seen.Format("15:04:05")
		}
		reply = fmt.Sprintf("本群自然接话：%s。\n%s。\n开启后会等待约 5 秒安静，每次至少 3 条新消息再判断；尝试间隔至少 2 分钟，每小时最多 6 次，不保证每次发言。忙碌时跳过，明确 @ 的任务优先。\nQQ 群设置须允许“获取群内全部消息”，按平台要求开启“机器人主动在群聊内发言”。没有收到普通消息时不能免 @ 接话。\n%s", status, received, reply)
	} else if len(args) == 2 && (args[1] == "on" || args[1] == "off") {
		if !c.isAdmin(m.UnionOpenID, m.UserOpenID, m.MemberOpenID) {
			reply = "只有已配置的 QQ 管理员能修改本群自然接话开关。"
		} else if err := c.store.SetMeta(ctx, "qq:auto:"+scope, args[1]); err != nil {
			reply = "保存自然接话设置失败，请稍后重试。"
		} else {
			group := c.autoState(scope)
			group.cancelPending()
			group.messages = 0
			if args[1] == "on" {
				reply = "本群自然接话已开启。请在 QQ 群的机器人设置允许获取群内全部消息，并按平台要求允许主动发言。模型会选择是否接话；日常接话不执行工具，工作任务请明确 @ 我。/auto 查看是否收到普通消息，/auto off 关闭。"
			} else {
				reply = "本群自然接话已关闭，待发送的接话已取消。明确 @ 的任务照常处理。"
			}
		}
	}
	_ = sess.Notice(ctx, reply, target)
	return true
}

func (c *Channel) observeAuto(ctx context.Context, m InboundMessage, scope, sessionKey, display, target string) {
	if !c.autoEnabled(ctx, scope) {
		return
	}
	group := c.autoState(scope)
	// New input invalidates a pending optional answer. It must not cut across a
	// newer exchange or an explicit request. Commands addressed to others stay out.
	group.cancelPending()
	if strings.HasPrefix(strings.TrimSpace(m.Text), "/") {
		return
	}
	sess, err := c.session(sessionKey)
	if err != nil {
		return
	}
	doc := c.resolveInput(m, scope)
	stored, err := sess.Ingest(ctx, storage.Message{Channel: "qq", AuthorKind: "player", AuthorID: speakerID(m), AuthorName: display, Text: doc.summary(), Target: target})
	if err != nil {
		c.log.Warn("qq ordinary input", "err", err)
		return
	}
	group.messages++
	if m.HasOtherMention || group.messages < 3 {
		return
	}
	generation := group.generation
	group.timer = time.AfterFunc(autoQuiet, func() {
		c.routingMu.Lock()
		defer c.routingMu.Unlock()
		if group.generation == generation {
			c.flushAuto(scope, sessionKey, target, stored.ID)
		}
	})
}

func (c *Channel) flushAuto(scope, sessionKey, target string, trigger int64) {
	group := c.autoGroups[scope]
	if group == nil || c.closing {
		return
	}
	group.timer = nil
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !c.autoEnabled(ctx, scope) {
		return
	}
	active, err := c.store.ActiveQQConversation(ctx, scope, time.Now().UnixMilli())
	if err != nil || active.SessionID != sessionKey {
		return
	}
	now := time.Now()
	if now.Sub(group.lastAttempt) < autoCooldown {
		return
	}
	kept := group.attempts[:0]
	for _, at := range group.attempts {
		if now.Sub(at) < time.Hour {
			kept = append(kept, at)
		}
	}
	group.attempts = kept
	if len(kept) >= autoHourlyAttempts {
		return
	}
	running, waiting := c.ag.QueueStatus()
	if running+waiting != 0 {
		return
	}
	sess, err := c.session(sessionKey)
	if err != nil {
		return
	}
	// Optional chat expires quickly; explicit tool tasks retain their unlimited
	// budget. A new @, /stop, /new, /auto off or newer group input cancels it.
	task := c.newTask(scope, "", target, sessionKey)
	runCtx, stop := context.WithTimeout(task.ctx, 90*time.Second)
	group.cancel = func() { stop(); task.cancel() }
	instruction := prompts.Resolve("qq", "", qqInstruction) + "\n\n" + qqConversationStyle
	effort := firstNonEmpty(c.cfg.ReasoningEffort, "high")
	if selected, err := c.store.SessionEffort(ctx, sessionKey); err == nil && selected != "" {
		effort = selected
	}
	if !c.ag.Submit(agent.Request{Social: true, Context: runCtx, Session: sess, SessionKey: sessionKey, ReplyTarget: target, TriggerMessageID: trigger, ReasoningEffort: effort, SystemInstruction: instruction, ManagedProgress: true, Started: task.start, Done: func() { stop(); task.cancel(); c.finishTask(scope, task) }}) {
		stop()
		task.cancel()
		c.finishTask(scope, task)
		return
	}
	group.lastAttempt = now
	group.attempts = append(group.attempts, now)
	group.messages = 0
}
