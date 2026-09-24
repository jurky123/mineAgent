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
		hub:         hub,
		store:       store,
		ag:          ag,
		api:         api,
		qqTools:     qqTools,
		sessions:    make(map[string]*session.Session),
		lastSend:    make(map[string]time.Time),
		seen:        make(map[string]time.Time),
		minInterval: time.Duration(cfg.QQ.MinIntervalMS) * time.Millisecond,
	}
	c.gw = NewGateway(api, api.tokens, log, c.onMessage)
	return c
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

// sendImage 发 workspace 内图片：target="img:<c2c|group>:<id>:<msgID>:<relPath>"，
// caption 放 msg.Text（v1 图片不带 caption，只发图；caption 记日志备查）。
// 失败返回 error（agent 工具转告用户），不静默吞。
func (c *Channel) sendImage(ctx context.Context, target, caption string) error {
	parts := strings.SplitN(target, ":", 4)
	if len(parts) != 4 {
		c.log.Warn("qq image with bad target", "target", target)
		return nil
	}
	kind, id, msgID, relPath := parts[0], parts[1], parts[2], parts[3]
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

	// 昵称留痕（platform=qq）：只用于日志展示，与绑定隔离。
	// 绑定关系存 platform=qq_bind（见 tools.BindPlatform），两者不互相覆盖。
	now := time.Now().UnixMilli()
	for _, id := range []string{m.UnionOpenID, m.UserOpenID, m.MemberOpenID} {
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
	for _, id := range []string{m.UnionOpenID, m.UserOpenID, m.MemberOpenID} {
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

	qqID := firstNonEmpty(m.UnionOpenID, m.UserOpenID, m.MemberOpenID)
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

const qqInstruction = `你是 Minecraft 服务器「jzk 的服务器」的 QQ 聊天 AI 助手，同时也是一个能写代码、
执行代码的轻量 agent。说话对象可能是服主（QQ 私聊）或玩家群（QQ 群），消息格式为 [QQ名] 内容。

规则：
- 用简体中文回答，语气轻松友好。
- QQ 里回复可以稍长，但单条控制在 500 字内；不要用 Markdown 表格（纯文本语气）。
  需要版式（标题/列表/加粗）时调用 qq_markdown 发一条；需要发图时调用 qq_image。
- 发图流程：先用 workspace_write/exec 把图做到 workspace 里（png/jpg，20MB内），
  再调 qq_image 发，path 写相对路径。图片先分片上传再发送，上传失败会如实报错，
  不要编造"已发送"。
- 做多步任务（查数据->装包->画图->发图）时：每步一次只调一个工具，
  拿到结果再调下一步；画图直接用 PIL（已装），中文字体用
  /usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc（已装，别用黑体/DejaVu）。
  不要反复试探同一条失败命令，换一条路走；20 步内完不成就先回一条进度，再继续。
- 被问到服务器实时情况（在线玩家、TPS/内存、时间、天气）时必须先调用 minecraft_* 只读工具查，不要编造。
- minecraft_teleport / minecraft_give / minecraft_run_command 是高权限操作：只能应明确请求发起，
  发起后必须等待游戏内管理员批准；请求者没有绑定 MC 身份时要先提醒他用「绑定 <MC名>」绑定。
- workspace_ls / workspace_read / workspace_write / workspace_exec 是写代码和执行代码的工具，
  只能管理员（adminOpenIds）使用——非管理员调用会被直接拒绝，你不要绕过。
  所有操作都被限制在 workspace 目录内；执行命令有超时和输出上限。
  装依赖用 $VENV_BIN/pip install（只能装进 workspace/.venv），下载用 curl/wget（只允许从公开 http(s) 下载到 workspace 内）；
  这两类会先过静态约束再送 LLM 语义审查，审查不通过就执行不了——被拒时如实转告，不要编造结果。
  写文件前先 ls/read 确认，不要覆盖已有重要文件；exec 一次只做一件事，重要操作先 dry-run。
- 「绑定 <MC名>」是用户要绑定 MC 身份：调用 qq_bind 工具；「解绑」调用 qq_unbind。
- 不确定的服务器信息不要编造，直接说不知道。`
