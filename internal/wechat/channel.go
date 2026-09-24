package wechat

import (
	"context"
	"log/slog"
	"path/filepath"
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

// Channel 是「个人微信 ClawBot」通道（直连腾讯 iLink，不跑 OpenClaw）：
// 长轮询收消息 -> 会话隔离 -> agent -> sendmessage 回复（透传 context_token）。
//
// 会话隔离：wechat:c2c:<from_user_id>（群消息若带 group_id 则 wechat:group:<group_id>）。
// 回复目标与官方插件一致：回给 from_user_id，context_token 用该用户最近一次上行 token。
type Channel struct {
	log        *slog.Logger
	cfg        config.WeChat
	statePath  string
	model      string
	mcStatus   func(ctx context.Context) (string, error)

	hub     *session.Hub
	store   *storage.Store
	ag      *agent.Agent
	cli     *Client
	monitor *Monitor
	state   *State
	weTools []tool.BaseTool

	mu       sync.Mutex
	sessions map[string]*session.Session
	lastSend map[string]time.Time
	seen     map[string]time.Time

	minInterval time.Duration
}

func NewChannel(log *slog.Logger, cfg config.Config, statePath string, hub *session.Hub,
	store *storage.Store, ag *agent.Agent, weTools []tool.BaseTool) (*Channel, error) {
	state, err := LoadState(statePath)
	if err != nil {
		return nil, err
	}
	if state.Token == "" {
		return nil, nil // 未登录：由 main 决定是否提示
	}
	cli := NewClient(state.BaseURL, state.Token, cfg.WeChat.BotAgent)
	c := &Channel{
		log:         log,
		cfg:         cfg.WeChat,
		statePath:   statePath,
		model:       cfg.Model.Name,
		hub:         hub,
		store:       store,
		ag:          ag,
		cli:         cli,
		state:       state,
		weTools:     weTools,
		sessions:    make(map[string]*session.Session),
		lastSend:    make(map[string]time.Time),
		seen:        make(map[string]time.Time),
		minInterval: time.Duration(cfg.WeChat.MinIntervalMS) * time.Millisecond,
	}
	c.monitor = NewMonitor(cli, state, statePath, log, c.onMessage)
	return c, nil
}

func (c *Channel) Name() string { return "wechat" }

// Start 起长轮询（非阻塞，内部自带退避重试）。
func (c *Channel) Start() { c.monitor.Start() }

func (c *Channel) Stop() { c.monitor.Stop() }

// WithMCStatus 注入查服状态闭包（/status 直回用）。
func (c *Channel) WithMCStatus(fn func(ctx context.Context) (string, error)) *Channel {
	c.mcStatus = fn
	return c
}

// Status 供 /status。
func (c *Channel) Status() string {
	if c.state.ILinkUserID != "" {
		return "已登录（ilink_user_id=" + c.state.ILinkUserID + "）"
	}
	return "已登录"
}

// IsAdmin 判管理员：按登录者 ilink_user_id 或配置的 adminUserIds。
func (c *Channel) IsAdmin(userID string) bool {
	if userID == "" {
		return false
	}
	if c.state.ILinkUserID != "" && userID == c.state.ILinkUserID {
		return true
	}
	for _, id := range c.cfg.AdminUserIDs {
		if id != "" && id == userID {
			return true
		}
	}
	return false
}

// Send 实现 session.Channel：Target = "c2c:<userid>"（群聊同为发送者 userid，
// 与官方插件行为一致）；context_token 从 state 取。
func (c *Channel) Send(ctx context.Context, msg storage.Message) error {
	kind, id, _ := splitTarget(msg.Target)
	if kind != "c2c" && kind != "group" {
		c.log.Warn("wechat send with bad target", "target", msg.Target)
		return nil
	}
	c.mu.Lock()
	contextToken := c.state.ContextTokens[id]
	c.mu.Unlock()
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return nil
	}
	// 长文本分片发送（微信单条不宜过长），逐段串行。
	for _, chunk := range chunkRunes(text, 1500) {
		if err := c.cli.SendText(ctx, id, chunk, contextToken); err != nil {
			return err
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}

func (c *Channel) onMessage(m WeixinMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 去重：服务端可能重推同一 message_id。
	if m.MessageID != "" {
		c.mu.Lock()
		if _, ok := c.seen[m.MessageID]; ok {
			c.mu.Unlock()
			return
		}
		c.seen[m.MessageID] = time.Now()
		for id, t := range c.seen {
			if time.Since(t) > time.Hour {
				delete(c.seen, id)
			}
		}
		c.mu.Unlock()
	}

	text := ""
	for _, it := range m.ItemList {
		if it.Type == ItemTypeText && it.TextItem != nil {
			text += it.TextItem.Text
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	sessionKey := "wechat:c2c:" + m.FromUserID
	if m.GroupID != "" {
		sessionKey = "wechat:group:" + m.GroupID
	}
	target := "c2c:" + m.FromUserID

	// 限流：同一会话回复最小间隔。
	c.mu.Lock()
	last := c.lastSend[sessionKey]
	if time.Since(last) < c.minInterval {
		c.mu.Unlock()
		c.log.Debug("wechat rate limited", "session", sessionKey)
		return
	}
	c.mu.Unlock()

	sess, err := c.session(sessionKey)
	if err != nil {
		c.log.Error("wechat session", "err", err)
		return
	}

	// 系统命令硬编码直回。
	if cmd, arg := tools.MatchSystemCommand(text); cmd != "" {
		reply := tools.ExecSystemCommand(tools.SysCtx{
			Ctx:       ctx,
			Store:     c.store,
			SessionID: sessionKey,
			Channel:   "wechat",
			IsAdmin:   c.IsAdmin(m.FromUserID),
			Model:     c.model,
			MCStatus:  c.mcStatus,
			QQStatus:  c.Status,
			QQIDs:     []string{m.FromUserID},
			Requester: tools.WeComRequesterPrefix + m.FromUserID,
		}, cmd, arg)
		c.log.Info("wechat syscmd", "session", sessionKey, "cmd", cmd, "arg", arg)
		_ = sess.Reply(ctx, reply, target)
		c.monitor.SaveNow()
		c.mu.Lock()
		c.lastSend[sessionKey] = time.Now()
		c.mu.Unlock()
		return
	}

	now := time.Now().UnixMilli()
	_ = c.store.UpsertIdentity(ctx, "wechat", m.FromUserID, m.FromUserID, now)

	stored, err := sess.Ingest(ctx, storage.Message{
		Channel:    "wechat",
		AuthorKind: "player",
		AuthorID:   sessionKey,
		AuthorName: m.FromUserID,
		Text:       text,
		Target:     target,
	})
	if err != nil {
		c.log.Error("wechat ingest", "err", err)
		return
	}

	var mcName string
	if name, err := c.store.LinkedMC(ctx, tools.BindPlatform, m.FromUserID); err == nil && name != "" {
		mcName = name
	}

	c.log.Info("wechat trigger",
		"session", sessionKey, "author", m.FromUserID,
		"group", m.GroupID != "", "boundMC", mcName, "query", text, "messageId", stored.ID)

	if !c.ag.Submit(agent.Request{
		Session:           sess,
		SessionKey:        sessionKey,
		Player:            tools.WeComRequesterPrefix + m.FromUserID,
		RequesterID:       m.FromUserID,
		MCRequester:       mcName,
		Query:             text,
		TriggerMessageID:  stored.ID,
		ReplyTarget:       target,
		Tools:             c.weTools,
		SystemInstruction: wechatInstruction,
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

func splitTarget(target string) (kind, id, rest string) {
	parts := strings.SplitN(target, ":", 3)
	if len(parts) < 2 {
		return "", "", ""
	}
	if len(parts) == 2 {
		return parts[0], parts[1], ""
	}
	return parts[0], parts[1], parts[2]
}

// chunkRunes 按 rune 切分长文本（微信单条不宜过长）。
func chunkRunes(s string, max int) []string {
	r := []rune(s)
	if len(r) <= max {
		return []string{s}
	}
	var out []string
	for len(r) > 0 {
		n := max
		if len(r) < n {
			n = len(r)
		}
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return out
}

// StatePathFor 由存储路径推导微信状态文件路径（同目录 wechat.json）。
func StatePathFor(storagePath string) string {
	return filepath.Join(filepath.Dir(storagePath), "wechat.json")
}

const wechatInstruction = `你是 MineAgent，一个能写代码、执行代码、查资料、画图的轻量 agent，
当前通过「个人微信 ClawBot」跟人聊天（消息格式为 [userid] 内容）。
Minecraft 服务器「jzk 的服务器」只是你其中一个功能。

规则：
- 用简体中文回答，语气轻松友好；微信里单条不要超过 1500 字（超了系统会自动分片）。
- 不做 markdown 重排版（微信不适合表格），需要时用短段落/短列表。
- 做多步任务（查数据->装包->画图）时：每步一次只调一个工具，拿到结果再调下一步；
  不要反复试探同一条失败命令，换一条路走。
- MC 服务器只是功能之一：被问到服实时情况（在线玩家、TPS/内存/时间/天气）时才调用
  minecraft_* 只读工具查，不要编造。
- minecraft_teleport / minecraft_give / minecraft_run_command 是高权限操作：只能应明确请求发起，
  发起后必须等待游戏内管理员批准；请求者没有绑定 MC 身份时要先提醒他用「绑定 <MC名>」绑定。
- workspace_ls / workspace_read / workspace_write / workspace_exec 是写代码和执行代码的工具，
  只能管理员使用——非管理员调用会被直接拒绝，不要绕过。
  操作限制在 workspace 目录内；装依赖用 $VENV_BIN/pip install，下载用 curl/wget（公开 http(s)），
  这两类先过静态约束再送 LLM 语义审查。
- 对话管理命令（/help /status /memory /bind /unbind /myid）由系统层直接回复，
  不经过你；用户问起就照 help 文案介绍，不要自己编命令列表。
- v1 暂不支持收/发图片（个人微信 ClawBot 图片通道待后续版本）。
- 不确定的信息不要编造，直接说不知道。`
