package wecom

import (
	"context"
	"log/slog"
	"strconv"
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

// Channel 是企业微信通道：回调事件 -> 会话隔离 -> agent -> 主动 SendText 发回。
//
// 会话隔离：单聊按人（wecom:c2c:<userid>），群聊按群（wecom:group:<chatid>），
// 与 qq:/minecraft- 会话天然隔离。
//
// 与 QQ 的差异：
//   - 回复不走被动回包（那个只有 5 秒），统一走主动 SendText；
//     应用消息有频控（单成员 30 条/分钟），channel 用 minInterval 防抖。
//   - 图片消息：v1 只支持 agent 发 workspace 内 jpg/png（上传临时素材 -> media_id），
//     用户发来的图片事件跳过（日志 debug）。
//   - requester 前缀 wecom:，复用 QQ 同款外部审批路径。
//   - 管理员用企业内明文 userid（AdminUserIDs）判定。
type Channel struct {
	log *slog.Logger
	cfg config.WeCom
	// workspaceRoot 发本地图片定位 workspace。
	workspaceRoot string
	model         string
	mcStatus      func(ctx context.Context) (string, error)

	hub     *session.Hub
	store   *storage.Store
	ag      *agent.Agent
	api     *API
	tokens  *TokenSource
	cb      *CallbackServer
	weTools []tool.BaseTool

	mu       sync.Mutex
	sessions map[string]*session.Session
	lastSend map[string]time.Time
	seen     map[string]time.Time

	minInterval time.Duration
}

func NewChannel(log *slog.Logger, cfg config.Config, hub *session.Hub, store *storage.Store,
	ag *agent.Agent, api *API, weTools []tool.BaseTool) *Channel {
	c := &Channel{
		log:           log,
		cfg:           cfg.WeCom,
		workspaceRoot: cfg.WorkspaceRoot(),
		model:         cfg.Model.Name,
		hub:           hub,
		store:         store,
		ag:            ag,
		api:           api,
		tokens:        api.Tokens(),
		weTools:       weTools,
		sessions:      make(map[string]*session.Session),
		lastSend:      make(map[string]time.Time),
		seen:          make(map[string]time.Time),
		minInterval:   time.Duration(cfg.WeCom.MinIntervalMS) * time.Millisecond,
	}
	c.cb = NewCallbackServer(Config{
		CorpID:      cfg.WeCom.CorpID,
		Token:       cfg.WeCom.Token,
		EncodingAES: cfg.WeCom.EncodingAES,
		Port:        cfg.WeCom.Port,
	}, log, c.onMessage).WithCorpIDLearner(func(id string) {
		if c.tokens != nil {
			c.tokens.SetCorpID(id)
		}
	})
	return c
}

func (c *Channel) Name() string { return "wecom" }

// Start 起回调 HTTP 服务（阻塞式调用方自己 go）。
func (c *Channel) Start() error { return c.cb.Start() }

func (c *Channel) Stop() { c.cb.Stop() }

// WithMCStatus 注入查服状态闭包（/status 直回用）。
func (c *Channel) WithMCStatus(fn func(ctx context.Context) (string, error)) *Channel {
	c.mcStatus = fn
	return c
}

// WeComStatus 供 /status：回调服务已监听即视为可用；corpId 未知时标注。
func (c *Channel) WeComStatus() string {
	if c.cb == nil {
		return "未启动"
	}
	s := "回调已监听（端口" + strconv.Itoa(c.cfg.Port) + "）"
	if c.tokens != nil && c.tokens.CorpID() == "" {
		s += "，corpId 未学到（发条消息即自动学）"
	}
	return s
}

// isAdmin 判企微管理员：明文 userid 命中 adminUserIds。
func (c *Channel) isAdmin(userID string) bool {
	for _, id := range c.cfg.AdminUserIDs {
		if id != "" && id == userID {
			return true
		}
	}
	return false
}

// Send 实现 session.Channel：把 agent 回复发回企微。
// 纯文本 Target="c2c:<userid>" 或 "group:<chatid>"；
// markdown Target="md:<c2c|group>:<id>"；图片 Target="img:<c2c|group>:<id>:<workspace相对路径>"。
// 与 QQ 不同：没有 msgID 被动窗口，全部走主动消息。
func (c *Channel) Send(ctx context.Context, msg storage.Message) error {
	target := msg.Target
	if strings.HasPrefix(target, storage.KindMarkdown) {
		return c.sendMarkdown(ctx, strings.TrimPrefix(target, storage.KindMarkdown), firstLine(msg.Text, 4000))
	}
	if strings.HasPrefix(target, storage.KindImage) {
		return c.sendImage(ctx, strings.TrimPrefix(target, storage.KindImage))
	}
	kind, id, _ := splitTarget(target)
	switch kind {
	case "c2c":
		return c.api.SendText(ctx, id, "", firstLine(msg.Text, 2000))
	case "group":
		return c.api.SendText(ctx, "", id, firstLine(msg.Text, 2000))
	default:
		c.log.Warn("wecom send with bad target", "target", target)
		return nil
	}
}

func (c *Channel) sendMarkdown(ctx context.Context, target, markdown string) error {
	if strings.TrimSpace(markdown) == "" {
		return nil
	}
	kind, id, _ := splitTarget(target)
	switch kind {
	case "c2c":
		return c.api.SendMarkdown(ctx, id, "", markdown)
	case "group":
		return c.api.SendMarkdown(ctx, "", id, markdown)
	default:
		c.log.Warn("wecom markdown with bad target", "target", target)
		return nil
	}
}

func (c *Channel) sendImage(ctx context.Context, target string) error {
	kind, id, relPath := splitImageTarget(target)
	if kind == "" {
		c.log.Warn("wecom image with bad target", "target", target)
		return nil
	}
	upCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	mediaID, err := c.api.UploadImage(upCtx, c.workspaceRoot, relPath)
	if err != nil {
		c.log.Warn("wecom image upload failed", "target", target, "err", err)
		return err
	}
	if kind == "c2c" {
		return c.api.SendImage(ctx, id, "", mediaID)
	}
	return c.api.SendImage(ctx, "", id, mediaID)
}

func (c *Channel) onMessage(m InboundMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 去重：平台网络失败会重试，同 msgid 只处理一次。
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

	var sessionKey, display, target string
	switch m.Kind {
	case "c2c":
		if m.UserID == "" {
			return
		}
		sessionKey = "wecom:c2c:" + m.UserID
		display = m.UserID
		target = "c2c:" + m.UserID
	case "group":
		if m.GroupChatID == "" {
			return
		}
		sessionKey = "wecom:group:" + m.GroupChatID
		display = m.UserID
		target = "group:" + m.GroupChatID
	default:
		return
	}

	// 限流：同一会话两次回复至少间隔 minInterval。
	c.mu.Lock()
	last := c.lastSend[sessionKey]
	if time.Since(last) < c.minInterval {
		c.mu.Unlock()
		c.log.Debug("wecom rate limited", "session", sessionKey)
		return
	}
	c.mu.Unlock()

	sess, err := c.session(sessionKey)
	if err != nil {
		c.log.Error("wecom session", "err", err)
		return
	}

	// 系统命令硬编码直回（与 QQ/MC 一致），不进 agent 队列。
	isAdmin := c.isAdmin(m.UserID)
	if cmd, arg := tools.MatchSystemCommand(text); cmd != "" {
		reply := tools.ExecSystemCommand(tools.SysCtx{
			Ctx:       ctx,
			Store:     c.store,
			SessionID: sessionKey,
			Channel:   "wecom",
			IsAdmin:   isAdmin,
			Model:     c.model,
			MCStatus:  c.mcStatus,
			QQStatus:  nil,
			QQIDs:     []string{m.UserID},
			Requester: tools.WeComRequesterPrefix + m.UserID,
		}, cmd, arg)
		c.log.Info("wecom syscmd", "session", sessionKey, "cmd", cmd, "arg", arg)
		_ = sess.Reply(ctx, reply, target)
		c.mu.Lock()
		c.lastSend[sessionKey] = time.Now()
		c.mu.Unlock()
		return
	}

	// 昵称留痕（platform=wecom）：回调只给 userid，写一份便于日志/绑定一致。
	now := time.Now().UnixMilli()
	_ = c.store.UpsertIdentity(ctx, "wecom", m.UserID, display, now)

	stored, err := sess.Ingest(ctx, storage.Message{
		Channel:    "wecom",
		AuthorKind: "player",
		AuthorID:   sessionKey,
		AuthorName: display,
		Text:       text,
		Target:     target,
	})
	if err != nil {
		c.log.Error("wecom ingest", "err", err)
		return
	}

	// 查绑定的 MC 名：与 /bind 命令写入同一个 platform_id（明文 userid）。
	var mcName string
	if name, err := c.store.LinkedMC(ctx, tools.BindPlatform, m.UserID); err == nil && name != "" {
		mcName = name
	}

	c.log.Info("wecom trigger",
		"session", sessionKey, "author", display,
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
		Tools:             c.weTools,
		SystemInstruction: wecomInstruction,
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

// splitTarget 切 "<c2c|group>:<id>"（企微没有 msgID 段）。
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

// splitImageTarget 切 "<c2c|group>:<id>:<relPath>"。
func splitImageTarget(target string) (kind, id, relPath string) {
	parts := strings.SplitN(target, ":", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	if parts[0] != "c2c" && parts[0] != "group" {
		return "", "", ""
	}
	if parts[2] == "" || strings.Contains(parts[2], ":") {
		return "", "", ""
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

const wecomInstruction = `你是 MineAgent，一个能写代码、执行代码、查资料、画图发图的轻量 agent，
当前在企业微信里跟人聊天（可能是单聊，也可能是应用群聊，消息格式为 [userid] 内容）。
你的能力很多，Minecraft 服务器「jzk 的服务器」只是其中一个可用的功能（查服状态、
传送/给物/执行命令需审批），不要把自己只当成服的客服。

规则：
- 用简体中文回答，语气轻松友好。
- 企微里回复可以稍长，但单条控制在 500 字内；不要用 Markdown 表格。
  需要版式时调用 wecom_markdown 发一条；需要发图时调用 wecom_image。
- 发图流程：先用 workspace_write/exec 把图做到 workspace 里（jpg/png，10MB内），
  再调 wecom_image 发，path 写相对路径。上传失败会如实报错，不要编造"已发送"。
- 做多步任务（查数据->装包->画图->发图）时：每步一次只调一个工具，
  拿到结果再调下一步；画图直接用 PIL（已装），中文字体用
  /usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc（已装，别用黑体/DejaVu）。
  不要反复试探同一条失败命令，换一条路走；20 步内完不成就先回一条进度，再继续。
- MC 服务器只是功能之一：被问到服实时情况（在线玩家、TPS/内存、时间、天气）时才调用
  minecraft_* 只读工具查，不要编造；平时聊天、写代码、查资料都不用碰 MC。
- minecraft_teleport / minecraft_give / minecraft_run_command 是高权限操作：只能应明确请求发起，
  发起后必须等待游戏内管理员批准；请求者没有绑定 MC 身份时要先提醒他用「绑定 <MC名>」绑定。
- workspace_ls / workspace_read / workspace_write / workspace_exec 是写代码和执行代码的工具，
  只能管理员（adminUserIds）使用——非管理员调用会被直接拒绝，你不要绕过。
  所有操作都被限制在 workspace 目录内；执行命令有超时和输出上限。
  装依赖用 $VENV_BIN/pip install（只能装进 workspace/.venv），下载用 curl/wget（只允许从公开 http(s) 下载到 workspace 内）；
  这两类会先过静态约束再送 LLM 语义审查，审查不通过就执行不了——被拒时如实转告，不要编造结果。
  写文件前先 ls/read 确认，不要覆盖已有重要文件；exec 一次只做一件事，重要操作先 dry-run。
- 对话管理命令（/help /status /memory /bind /unbind /myid）由系统层直接回复，
  不经过你：如果用户问起这些命令，你照着 help 文案介绍，不要自己编命令列表。
- 不确定的信息不要编造，直接说不知道。`
