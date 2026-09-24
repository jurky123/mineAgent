package aibot

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"mineagent/internal/agent"
	"mineagent/internal/config"
	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

// Channel 是「企微智能机器人（长连接）」通道：WS 回调 -> 会话隔离 -> agent -> 回复。
//
// 会话隔离：单聊按人（aibot:c2c:<userid>），群聊按群（aibot:group:<chatid>），
// 与 qq:/wecom:/minecraft- 会话互不干扰。
//
// 与自建应用通道（internal/wecom）的差异：
//   - 无需公网回调/加解密，机器人主动连 wss://openws.work.weixin.qq.com。
//   - 回复默认走被动 aibot_respond_msg（透传回调 req_id，24h 窗口）；
//     失败或窗口过期自动降级 aibot_send_msg（主动推送，需用户先发过消息）。
//   - 可被扫码加为联系人、可进内部群被 @（chattype=group）。
type Channel struct {
	log *slog.Logger
	cfg config.AIBot
	// workspaceRoot 发本地图片定位 workspace（v1 aibot 暂不支持发图）。
	workspaceRoot string
	model         string
	mcStatus      func(ctx context.Context) (string, error)

	hub    *session.Hub
	store  *storage.Store
	ag     *agent.Agent
	client *Client
	tools  []tool.BaseTool

	mu       sync.Mutex
	sessions map[string]*session.Session
	lastSend map[string]time.Time
	seen     map[string]time.Time

	minInterval time.Duration
}

func NewChannel(log *slog.Logger, cfg config.Config, hub *session.Hub, store *storage.Store,
	ag *agent.Agent, aiTools []tool.BaseTool) *Channel {
	c := &Channel{
		log:           log,
		cfg:           cfg.AIBot,
		workspaceRoot: cfg.WorkspaceRoot(),
		model:         cfg.Model.Name,
		hub:           hub,
		store:         store,
		ag:            ag,
		tools:         aiTools,
		sessions:      make(map[string]*session.Session),
		lastSend:      make(map[string]time.Time),
		seen:          make(map[string]time.Time),
		minInterval:   time.Duration(cfg.AIBot.MinIntervalMS) * time.Millisecond,
	}
	c.client = NewClient(cfg.AIBot.BotID, cfg.AIBot.Secret, log, c.onMessage)
	return c
}

func (c *Channel) Name() string { return "aibot" }

// Start 起长连接（非阻塞，内部自带重连循环）。
func (c *Channel) Start() { c.client.Start() }

func (c *Channel) Stop() { c.client.Stop() }

// WithMCStatus 注入查服状态闭包（/status 直回用）。
func (c *Channel) WithMCStatus(fn func(ctx context.Context) (string, error)) *Channel {
	c.mcStatus = fn
	return c
}

// AIBotStatus 供 /status。
func (c *Channel) AIBotStatus() string {
	if c.client != nil && c.client.Connected() {
		return "长连接已订阅"
	}
	return "未连接（等重连）"
}

func (c *Channel) isAdmin(userID string) bool {
	for _, id := range c.cfg.AdminUserIDs {
		if id != "" && id == userID {
			return true
		}
	}
	return false
}

// Send 实现 session.Channel。Target 约定：
//
//	"c2c:<userid>:<reqid>"   —— 单聊被动回复（reqid 透传回调 req_id）
//	"group:<chatid>:<reqid>" —— 群聊被动回复
//
// reqid 为 "-" 时表示无被动上下文，直接走主动推送。
func (c *Channel) Send(ctx context.Context, msg storage.Message) error {
	kind, id, reqID := splitTarget(msg.Target)
	text := firstLine(msg.Text, 20000)
	switch kind {
	case "c2c":
		if reqID != "" && reqID != "-" {
			if err := c.client.Respond(ctx, reqID, text); err == nil {
				return nil
			} else {
				c.log.Warn("aibot respond failed, fallback to active", "err", err)
			}
		}
		return c.client.SendActive(ctx, id, chatTypeSingle, text)
	case "group":
		if reqID != "" && reqID != "-" {
			if err := c.client.Respond(ctx, reqID, text); err == nil {
				return nil
			} else {
				c.log.Warn("aibot group respond failed, fallback to active", "err", err)
			}
		}
		return c.client.SendActive(ctx, id, chatTypeGroup, text)
	default:
		c.log.Warn("aibot send with bad target", "target", msg.Target)
		return nil
	}
}

func (c *Channel) onMessage(m InboundMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 去重：平台可能重推同一 msgid。
	if m.MsgID != "" {
		c.mu.Lock()
		if _, ok := c.seen[m.MsgID]; ok {
			c.mu.Unlock()
			return
		}
		c.seen[m.MsgID] = time.Now()
		for id, t := range c.seen {
			if time.Since(t) > time.Hour {
				delete(c.seen, id)
			}
		}
		c.mu.Unlock()
	}

	text := strings.TrimSpace(m.Text)
	if text == "" {
		return
	}

	var sessionKey, target string
	switch m.Kind {
	case "c2c":
		if m.UserID == "" {
			return
		}
		sessionKey = "aibot:c2c:" + m.UserID
		target = "c2c:" + m.UserID + ":" + m.ReqID
	case "group":
		if m.ChatID == "" {
			return
		}
		sessionKey = "aibot:group:" + m.ChatID
		target = "group:" + m.ChatID + ":" + m.ReqID
	default:
		return
	}

	// 限流：同一会话两次回复至少间隔 minInterval（也避开企微 30 条/分钟限频）。
	c.mu.Lock()
	last := c.lastSend[sessionKey]
	if time.Since(last) < c.minInterval {
		c.mu.Unlock()
		c.log.Debug("aibot rate limited", "session", sessionKey)
		return
	}
	c.mu.Unlock()

	sess, err := c.session(sessionKey)
	if err != nil {
		c.log.Error("aibot session", "err", err)
		return
	}

	// 系统命令硬编码直回（与 QQ/企微一致），不进 agent 队列。
	if cmd, arg := tools.MatchSystemCommand(text); cmd != "" {
		reply := tools.ExecSystemCommand(tools.SysCtx{
			Ctx:       ctx,
			Store:     c.store,
			SessionID: sessionKey,
			Channel:   "aibot",
			IsAdmin:   c.isAdmin(m.UserID),
			Model:     c.model,
			MCStatus:  c.mcStatus,
			QQStatus:  c.AIBotStatus,
			QQIDs:     []string{m.UserID},
			Requester: tools.WeComRequesterPrefix + m.UserID,
		}, cmd, arg)
		c.log.Info("aibot syscmd", "session", sessionKey, "cmd", cmd, "arg", arg)
		_ = sess.Reply(ctx, reply, target)
		c.mu.Lock()
		c.lastSend[sessionKey] = time.Now()
		c.mu.Unlock()
		return
	}

	// 昵称留痕（platform=aibot，与 wecom 区分，避免 userid 互相覆盖展示名）。
	now := time.Now().UnixMilli()
	_ = c.store.UpsertIdentity(ctx, "aibot", m.UserID, m.UserID, now)

	stored, err := sess.Ingest(ctx, storage.Message{
		Channel:    "aibot",
		AuthorKind: "player",
		AuthorID:   sessionKey,
		AuthorName: m.UserID,
		Text:       text,
		Target:     target,
	})
	if err != nil {
		c.log.Error("aibot ingest", "err", err)
		return
	}

	// 查绑定的 MC 名（与 /bind 命令同一个 platform_id 命名）。
	var mcName string
	if name, err := c.store.LinkedMC(ctx, tools.BindPlatform, m.UserID); err == nil && name != "" {
		mcName = name
	}

	c.log.Info("aibot trigger",
		"session", sessionKey, "author", m.UserID,
		"boundMC", mcName, "query", text, "messageId", stored.ID)

	if !c.ag.Submit(agent.Request{
		Session:           sess,
		SessionKey:        sessionKey,
		Player:            tools.WeComRequesterPrefix + m.UserID,
		RequesterID:       m.UserID,
		MCRequester:       mcName,
		Query:             text,
		TriggerMessageID:  stored.ID,
		ReplyTarget:       target,
		Tools:             c.tools,
		SystemInstruction: aibotInstruction,
	}) {
		_ = sess.Reply(ctx, "抱歉，我现在忙不过来了，稍后再试。", target)
		return
	}

	c.mu.Lock()
	c.lastSend[sessionKey] = time.Now()
	c.mu.Unlock()
}

func (c *Channel) session(key string) (*session.Session, error) {
	c.mu.Lock()
	if s, ok := c.sessions[key]; ok {
		c.mu.Unlock()
		return s, nil
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := c.hub.Session(ctx, key)
	if err != nil {
		return nil, err
	}
	s.Register(c)
	c.mu.Lock()
	c.sessions[key] = s
	c.mu.Unlock()
	return s, nil
}

// splitTarget 切 "<c2c|group>:<id>[:<reqid>]"。
func splitTarget(target string) (kind, id, reqID string) {
	parts := strings.SplitN(target, ":", 3)
	if len(parts) < 2 {
		return "", "", ""
	}
	if len(parts) == 2 {
		return parts[0], parts[1], ""
	}
	return parts[0], parts[1], parts[2]
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

const aibotInstruction = `你是 MineAgent，一个能写代码、执行代码、查资料、画图的轻量 agent，
当前在企业微信「智能机器人」里跟人聊天（可能是单聊，也可能是被 @ 的群聊，
消息格式为 [userid] 内容）。Minecraft 服务器「jzk 的服务器」只是你其中一个功能。

规则：
- 用简体中文回答，语气轻松友好。
- 回复支持 markdown（标题/加粗/列表/引用/链接/代码块），单条 20000 字节内。
- 群聊里被 @ 才回复；回复直接发在群里，语气可以照顾到所有人。
- 做多步任务（查数据->装包->画图）时：每步一次只调一个工具，
  拿到结果再调下一步；画图直接用 PIL（已装），中文字体用
  /usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc。
  不要反复试探同一条失败命令，换一条路走；20 步内完不成就先回一条进度。
- MC 服务器只是功能之一：被问到服实时情况（在线玩家、TPS/内存/时间/天气）时才调用
  minecraft_* 只读工具查，不要编造。
- minecraft_teleport / minecraft_give / minecraft_run_command 是高权限操作：只能应明确请求发起，
  发起后必须等待游戏内管理员批准；请求者没有绑定 MC 身份时要先提醒他用「绑定 <MC名>」绑定。
- workspace_ls / workspace_read / workspace_write / workspace_exec 是写代码和执行代码的工具，
  只能管理员（adminUserIds）使用——非管理员调用会被直接拒绝，你不要绕过。
  所有操作都被限制在 workspace 目录内；装依赖用 $VENV_BIN/pip install，
  下载用 curl/wget（公开 http(s)），这两类会先过静态约束再送 LLM 语义审查。
- 对话管理命令（/help /status /memory /bind /unbind /myid）由系统层直接回复，
  不经过你；用户问起就照 help 文案介绍，不要自己编命令列表。
- 不确定的信息不要编造，直接说不知道。
- 注：v1 暂不支持发图片（发图请用企业微信自建应用通道）。`
