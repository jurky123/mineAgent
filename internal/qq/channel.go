package qq

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
	"mineagent/internal/usage"
)

// Channel 是 QQ 通道：网关事件 -> 会话隔离 -> agent -> 发回 QQ。
//
// 会话隔离（用户定的）：C2C 按人（qq:c2c:<user_openid>），群按群
// （qq:group:<group_openid>），MC 主会话（minecraft-main）保持全服共享不变。
// QQ 消息不进 MC 会话，MC 聊天也不进 QQ 会话，天然隔离。
//
// 触发规则：C2C 全部触发（单聊本来就是找机器人说话）；群聊只有 @机器人的
// 事件才会推过来（GROUP_AT_MESSAGE_CREATE），content 已去@前缀，直接触发。
//
// 安全：
//   - 同一会话回复最小间隔 minIntervalMs，防刷屏/防循环。
//   - 去重：同一 msgID 只处理一次（平台可能重推，见消息收发概述）。
//   - QQ 发起的 MC 高权限工具（teleport/give/run_command）跳过"本人权限预检"
//     直接转游戏内管理员审批（privileged.go 的 qq: 前缀分支），并受
//     maxPendingPerUser 限制防刷审批。
//   - workspace 写代码/执行代码仅 adminOpenIDs 可用，见 tools.WorkspaceTools。
type Channel struct {
	log *slog.Logger
	cfg config.QQ
	// workspaceRoot 发本地图片时定位 workspace（channel 拿的是整个 config，
	// 见 NewChannel）。默认 "workspace"。
	workspaceRoot string
	model         string
	mcStatus      func(ctx context.Context) (string, error)
	usage         *usage.Client

	hub     *session.Hub
	store   *storage.Store
	ag      *agent.Agent
	api     *API
	gw      *Gateway
	qqTools []tool.BaseTool

	mu       sync.Mutex
	sessions map[string]*session.Session
	lastSend map[string]time.Time
	seen     map[string]time.Time

	minInterval time.Duration
}

func NewChannel(log *slog.Logger, cfg config.Config, hub *session.Hub, store *storage.Store,
	ag *agent.Agent, api *API, qqTools []tool.BaseTool) *Channel {
	c := &Channel{
		log:           log,
		cfg:           cfg.QQ,
		workspaceRoot: cfg.WorkspaceRoot(),
		model:         cfg.Model.Name,
		usage:         usage.New(cfg.Model.BaseURL, cfg.Model.APIKey),
		hub:           hub,
		store:         store,
		ag:            ag,
		api:           api,
		qqTools:       qqTools,
		sessions:      make(map[string]*session.Session),
		lastSend:      make(map[string]time.Time),
		seen:          make(map[string]time.Time),
		minInterval:   time.Duration(cfg.QQ.MinIntervalMS) * time.Millisecond,
	}
	c.gw = NewGateway(api, api.tokens, log, c.onMessage)
	return c
}

// WithMCStatus 注入查服状态闭包（main.go 经 tools.Gateway 调 minecraft_server_status）。
func (c *Channel) WithMCStatus(fn func(ctx context.Context) (string, error)) *Channel {
	c.mcStatus = fn
	return c
}

// isAdmin 判 QQ 管理员：union/user/member 任一命中 adminOpenIds。
func (c *Channel) isAdmin(ids ...string) bool {
	for _, id := range ids {
		if id == "" {
			continue
		}
		for _, admin := range c.cfg.AdminOpenIDs {
			if admin != "" && id == admin {
				return true
			}
		}
	}
	return false
}

// QQStatus 供 /status：已连接/未连接。
func (c *Channel) QQStatus() string {
	if c.gw != nil && c.gw.Connected() {
		return "已连接"
	}
	return "未连接（等网关重连）"
}

func (c *Channel) Name() string { return "qq" }

func (c *Channel) Start() { c.gw.Start() }

func (c *Channel) Stop() { c.gw.Stop() }

// Send 实现 session.Channel：agent 的回复经这里发回 QQ。
// 纯文本 Target="c2c:<openid>:<msgID>" 或 "group:<groupid>:<msgID>"。
// markdown Target="md:<c2c|group>:<id>:<msgID>"，正文放 Text（msg_type=2）。
// 图片 Target="img:<c2c|group>:<id>:<msgID>:<workspace相对路径>"，走本地分片上传后发 msg_type=7。
// 被动回复优先带 msg_id（C2C 60分钟/群 5分钟内有效），过期了 API 会报错，
// 那就降级成主动消息再试一次。
func (c *Channel) Send(ctx context.Context, msg storage.Message) error {
	target := msg.Target
	kind := storage.KindText
	if strings.HasPrefix(target, storage.KindMarkdown) {
		kind = storage.KindMarkdown
		target = strings.TrimPrefix(target, storage.KindMarkdown)
	} else if strings.HasPrefix(target, storage.KindImage) {
		kind = storage.KindImage
		target = strings.TrimPrefix(target, storage.KindImage)
	}
	switch kind {
	case storage.KindMarkdown:
		return c.sendMarkdown(ctx, target, firstLine(msg.Text, 4000))
	case storage.KindImage:
		return c.sendImage(ctx, target, msg.Text)
	default:
		return c.sendText(ctx, target, firstLine(msg.Text, 1000))
	}
}

func (c *Channel) sendText(ctx context.Context, target, text string) error {
	kind, id, msgID := splitTarget(target)
	switch kind {
	case "c2c":
		if msgID != "" {
			if err := c.api.SendC2C(ctx, id, text, msgID, 1); err == nil {
				return nil
			} else {
				c.log.Warn("qq c2c passive reply failed, fallback to active", "err", err)
			}
		}
		return c.api.SendC2C(ctx, id, text, "", 0)
	case "group":
		if msgID != "" {
			if err := c.api.SendGroup(ctx, id, text, msgID, 1); err == nil {
				return nil
			} else {
				c.log.Warn("qq group passive reply failed, fallback to active", "err", err)
			}
		}
		return c.api.SendGroup(ctx, id, text, "", 0)
	default:
		c.log.Warn("qq send with bad target", "target", target)
		return nil
	}
}

// sendMarkdown 发 markdown（msg_type=2）。失败不降级纯文本：
// 版式乱了比多发一条更糟，调用方（agent 工具）收到 error 自会决定重试。
func (c *Channel) sendMarkdown(ctx context.Context, target, markdown string) error {
	if strings.TrimSpace(markdown) == "" {
		return nil
	}
	kind, id, msgID := splitTarget(target)
	switch kind {
	case "c2c":
		if msgID != "" {
			if err := c.api.SendC2CMarkdown(ctx, id, markdown, msgID, 1); err == nil {
				return nil
			} else {
				c.log.Warn("qq c2c markdown passive failed, fallback to active", "err", err)
			}
		}
		return c.api.SendC2CMarkdown(ctx, id, markdown, "", 0)
	case "group":
		if msgID != "" {
			if err := c.api.SendGroupMarkdown(ctx, id, markdown, msgID, 1); err == nil {
				return nil
			} else {
				c.log.Warn("qq group markdown passive failed, fallback to active", "err", err)
			}
		}
		return c.api.SendGroupMarkdown(ctx, id, markdown, "", 0)
	default:
		c.log.Warn("qq markdown with bad target", "target", target)
		return nil
	}
}

// sendImage 发 workspace 内图片。Send 调进来时 target 已剥掉 "img:" 前缀，
// 格式 "<c2c|group>:<id>:<msgID>:<relPath>"——注意 msgID 是 ROBOT1.0_...，
// 里面本身就带冒号，所以必须 SplitN(: , 5) 再取 [1:3] 拼回 msgID。
// caption 放 msg.Text（v1 图片不带 caption，只发图；caption 记日志备查）。
// 失败返回 error（agent 工具转告用户），不静默吞。
func (c *Channel) sendImage(ctx context.Context, target, caption string) error {
	kind, id, msgID, relPath := splitImageTarget(target)
	if kind == "" {
		c.log.Warn("qq image with bad target", "target", target)
		return nil
	}
	if relPath == "" {
		c.log.Warn("qq image with empty path", "target", target)
		return nil
	}
	// 上传+发送整体限 90s（分片 PUT 可能慢），ctx 是 agent 的 3min run ctx，够用。
	upCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var fileInfo string
	var err error
	switch kind {
	case "c2c":
		fileInfo, err = c.api.UploadC2CLocalImage(upCtx, id, c.workspaceRoot, relPath)
	case "group":
		fileInfo, err = c.api.UploadGroupLocalImage(upCtx, id, c.workspaceRoot, relPath)
	default:
		c.log.Warn("qq image with bad kind", "target", target)
		return nil
	}
	if err != nil {
		c.log.Warn("qq image upload failed", "target", target, "err", err, "caption", firstLine(caption, 100))
		return err
	}
	switch kind {
	case "c2c":
		if msgID != "" {
			if e := c.api.SendC2CMedia(ctx, id, fileInfo, msgID, 1); e == nil {
				return nil
			} else {
				c.log.Warn("qq c2c media passive failed, fallback to active", "err", e)
			}
		}
		return c.api.SendC2CMedia(ctx, id, fileInfo, "", 0)
	default:
		if msgID != "" {
			if e := c.api.SendGroupMedia(ctx, id, fileInfo, msgID, 1); e == nil {
				return nil
			} else {
				c.log.Warn("qq group media passive failed, fallback to active", "err", e)
			}
		}
		return c.api.SendGroupMedia(ctx, id, fileInfo, "", 0)
	}
}

func (c *Channel) onMessage(m InboundMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 去重：平台可能重推同一 msg_id。
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

	text := strings.TrimSpace(m.Text)
	if text == "" {
		return
	}

	var sessionKey, display, target string
	switch m.Kind {
	case "c2c":
		if m.UserOpenID == "" {
			return
		}
		sessionKey = "qq:c2c:" + m.UserOpenID
		display = firstNonEmpty(m.Username, "QQ用户")
		target = "c2c:" + m.UserOpenID + ":" + m.MsgID
	case "group_at":
		if m.GroupOpenID == "" {
			return
		}
		sessionKey = "qq:group:" + m.GroupOpenID
		display = firstNonEmpty(m.Username, "群友")
		target = "group:" + m.GroupOpenID + ":" + m.MsgID
	default:
		return
	}

	// 限流：同一会话两次回复至少间隔 minInterval。
	c.mu.Lock()
	last := c.lastSend[sessionKey]
	if time.Since(last) < c.minInterval {
		c.mu.Unlock()
		c.log.Debug("qq rate limited", "session", sessionKey)
		return
	}
	c.mu.Unlock()

	sess, err := c.session(sessionKey)
	if err != nil {
		c.log.Error("qq session", "err", err)
		return
	}

	// 系统命令硬编码直回：不进 agent 队列，不调 LLM，直接回。
	// QQ 管理员身份按 union/user/member 任一命中 adminOpenIds 判定。
	qqIDs := []string{m.UnionOpenID, m.UserOpenID, m.MemberOpenID}
	qqID := firstNonEmpty(qqIDs...)
	isAdmin := c.isAdmin(qqIDs...)
	if cmd, arg := tools.MatchSystemCommand(text); cmd != "" {
		reply := tools.ExecSystemCommand(tools.SysCtx{
			Ctx:       ctx,
			Store:     c.store,
			SessionID: sessionKey,
			Channel:   "qq",
			IsAdmin:   isAdmin,
			Model:     c.model,
			Usage:     c.usage.Text,
			MCStatus:  c.mcStatus,
			QQStatus:  c.QQStatus,
			QQIDs:     qqIDs,
			Requester: tools.QQRequesterPrefix + qqID,
		}, cmd, arg)
		c.log.Info("qq syscmd", "session", sessionKey, "cmd", cmd, "arg", arg)
		_ = sess.Reply(ctx, reply, target)
		c.mu.Lock()
		c.lastSend[sessionKey] = time.Now()
		c.mu.Unlock()
		return
	}

	// 昵称留痕（platform=qq）：只用于日志展示，与绑定隔离。
	// 绑定关系存 platform=qq_bind（见 tools.BindPlatform），两者不互相覆盖。
	now := time.Now().UnixMilli()
	for _, id := range qqIDs {
		if id == "" {
			continue
		}
		_ = c.store.UpsertIdentity(ctx, "qq", id, display, now)
	}

	stored, err := sess.Ingest(ctx, storage.Message{
		Channel:    "qq",
		AuthorKind: "player",
		AuthorID:   sessionKey,
		AuthorName: display,
		Text:       text,
		Target:     target,
	})
	if err != nil {
		c.log.Error("qq ingest", "err", err)
		return
	}

	// 查绑定的 MC 名：union -> user/member，有就带上，没有就空。
	// 读 platform=qq_bind（绑定专用），不读昵称留痕的 qq。
	// Gateway.Call 里 toolRequester 取它发给插件；approvalTool 审计仍用 qq: 身份。
	var mcName string
	for _, id := range qqIDs {
		if id == "" {
			continue
		}
		if name, err := c.store.LinkedMC(ctx, tools.BindPlatform, id); err == nil && name != "" {
			mcName = name
			break
		}
	}

	c.log.Info("qq trigger",
		"session", sessionKey, "author", display,
		"union", m.UnionOpenID != "", "boundMC", mcName,
		"query", text, "messageId", stored.ID)

	if !c.ag.Submit(agent.Request{
		Session:           sess,
		SessionKey:        sessionKey,
		Player:            tools.QQRequesterPrefix + qqID,
		RequesterID:       qqID,
		MCRequester:       mcName,
		Query:             text,
		TriggerMessageID:  stored.ID,
		ReplyTarget:       target,
		Tools:             c.qqTools,
		SystemInstruction: qqInstruction,
	}) {
		_ = sess.Reply(ctx, "抱歉，我现在忙不过来了，稍后再试。", target)
		return
	}

	c.mu.Lock()
	c.lastSend[sessionKey] = time.Now()
	c.mu.Unlock()
}

// Deliver 把一条消息发回指定会话（提醒等主动消息用；顺带注册本通道，
// 服务重启后会话未注册时也能送达）。
func (c *Channel) Deliver(ctx context.Context, sessionKey, target, text string) error {
	sess, err := c.session(sessionKey)
	if err != nil {
		return err
	}
	return sess.Reply(ctx, text, target)
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

func splitTarget(target string) (kind, id, msgID string) {
	parts := strings.SplitN(target, ":", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

// splitImageTarget 切 "<c2c|group>:<id>:<ROBOT1.0_...含点和下划线>:<path>"。
// 注意真实 msgID 是 ROBOT1.0_Dxxx.ts...Xy4tWZ...NnE! 形态——
// 点/下划线虽多但不含冒号，所以直接 SplitN(: ,4)：
// [kind, id, msgID, path]。path 不许含冒号/为空。
func splitImageTarget(target string) (kind, id, msgID, relPath string) {
	parts := strings.SplitN(target, ":", 4)
	if len(parts) != 4 {
		return "", "", "", ""
	}
	if parts[0] != "c2c" && parts[0] != "group" {
		return "", "", "", ""
	}
	if !strings.HasPrefix(parts[2], "ROBOT") {
		return "", "", "", ""
	}
	relPath = parts[3]
	if relPath == "" || strings.Contains(relPath, ":") {
		return "", "", "", ""
	}
	return parts[0], parts[1], parts[2], relPath
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	// 按 rune 截断，避免砍半个中文。
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

const qqInstruction = `你是 MineAgent，一个能动手的 agent（写代码/执行、查资料、画图、收发文件、联网搜索、
设提醒），当前在 QQ 里跟人聊天（可能是服主私聊，也可能是玩家群，消息格式为 [QQ名] 内容）。
Minecraft 服务器「jzk 的服务器」只是你能做的一件事（查状态、传送/给物/执行命令需审批），
不要把自己只当成服的客服。
` +
	agent.CoreAgentPrinciples + `
渠道规则（QQ）：
- 用简体中文回答，语气轻松友好；单条 500 字内，不要用 Markdown 表格。
  需要版式（标题/列表/加粗）时调用 qq_markdown 发一条；需要发图时调用 qq_image。
- 发图流程：先用 workspace_write/exec 把图做到 workspace 里（png/jpg，20MB内），
  再调 qq_image 发，path 写相对路径；上传失败会如实报错。
- 画图直接用 PIL（已装），中文字体用 /usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc
  （已装，别用黑体/DejaVu）。不要反复试探同一条失败命令，20 步内完不成就先回一条进度。
- MC 服务器只是功能之一：只在被问到服实时情况时才调 minecraft_* 工具，平时聊天/写代码/查资料不用碰 MC。
- minecraft_teleport / minecraft_give / minecraft_run_command 只能应明确请求发起，
  发起后等游戏内管理员批准；请求者没绑定 MC 身份时先提醒他用「绑定 <MC名>」。
- workspace_ls / workspace_read / workspace_write / workspace_exec 仅管理员（adminOpenIds）可用，
  非管理员调用会被直接拒绝，不要绕过。操作限制在 workspace 目录内，有超时和输出上限。
  装依赖用 $VENV_BIN/pip install（只进 workspace/.venv），下载用 curl/wget（仅公开 http(s) 到 workspace）；
  这两类先过静态约束再送 LLM 语义审查，被拒时如实转告。写文件前先 ls/read，重要操作先 dry-run。
- 对话管理命令（/help /status /memory /usage /bind /unbind /myid）由系统层直接回复，
  你照着 help 文案介绍，不要自己编命令列表。`
