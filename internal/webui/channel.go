package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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

// Channel 是网页通道：浏览器 -> 会话隔离 -> agent -> SSE 推回网页。
//
// 会话隔离：每个账号一个会话 web:c2c:<名字>（名字即账号，暂无密码）。
// 与 qq:/wecom:/minecraft- 会话天然隔离；同一会话多标签页共享消息（SSE 广播）。
//
// 收发文件：
//   - 用户上传：POST /api/upload 存到 workspace/web-files/<名字>/ 下，
//     消息文本里带 [[file:...]] 标记（agent 可见路径，前端显示卡片）。
//   - agent 发文件：web_file 工具（tools.WebSend）+ webToolGate，
//     消息 Target 前缀 file:，网页端内联图片/下载链接。
//
// 安全：账号无密码（按需求"暂时只用一个名字区分"），
// web.users 可配白名单；workspace/审批权限只给 web.adminUsers。
type Channel struct {
	log *slog.Logger
	cfg config.Web
	// workspaceRoot 上传目录/发文件都在它下面。
	workspaceRoot string
	model         string
	mcStatus      func(ctx context.Context) (string, error)

	hub      *session.Hub
	store    *storage.Store
	ag       *agent.Agent
	webTools []tool.BaseTool

	srv *http.Server

	mu       sync.Mutex
	sessions map[string]*session.Session
	lastSend map[string]time.Time
	// subs: 账号名 -> SSE 订阅者集合。
	subs map[string]map[chan []byte]struct{}
	// tokens: 账号名 -> 登录令牌列表（同一账号多设备并存，最多 5 个，最旧的淘汰；
	// 持久化到 tokensPath，重启不掉线）。
	tokens     map[string][]string
	tokensPath string
}

func NewChannel(log *slog.Logger, cfg config.Config, hub *session.Hub, store *storage.Store,
	ag *agent.Agent, webTools []tool.BaseTool) *Channel {
	dataDir := cfg.Web.DataDir
	if dataDir == "" {
		dataDir = "data/webui"
	}
	c := &Channel{
		log:           log,
		cfg:           cfg.Web,
		workspaceRoot: cfg.WorkspaceRoot(),
		model:         cfg.Model.Name,
		hub:           hub,
		store:         store,
		ag:            ag,
		webTools:      webTools,
		sessions:      make(map[string]*session.Session),
		lastSend:      make(map[string]time.Time),
		subs:          make(map[string]map[chan []byte]struct{}),
		tokens:        make(map[string][]string),
		tokensPath:    filepath.Join(dataDir, "tokens.json"),
	}
	c.loadTokens()
	return c
}

func (c *Channel) Name() string { return "web" }

// WithMCStatus 注入查服状态闭包（/status 直回用）。
func (c *Channel) WithMCStatus(fn func(ctx context.Context) (string, error)) *Channel {
	c.mcStatus = fn
	return c
}

// WebStatus 供 /status：监听地址 + 当前在线浏览器数。
func (c *Channel) WebStatus() string {
	if c.srv == nil {
		return "未启动"
	}
	c.mu.Lock()
	conns := 0
	for _, set := range c.subs {
		conns += len(set)
	}
	c.mu.Unlock()
	return fmt.Sprintf("已监听 %s（%d 个浏览器连接）", c.cfg.Listen, conns)
}

// Start 起 HTTP 服务（含静态页与 API），阻塞式，调用方自己 go。
func (c *Channel) Start() error { return c.Serve() }

func (c *Channel) Stop() {
	if c.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = c.srv.Shutdown(ctx)
	c.srv = nil
}

// Send 实现 session.Channel：agent 的回复 -> 对应账号的 SSE 订阅者。
// Target="c2c:<名字>"（file: 前缀是 agent 发的文件消息）。
func (c *Channel) Send(ctx context.Context, msg storage.Message) error {
	target := strings.TrimPrefix(msg.Target, storage.KindFile)
	target = strings.TrimPrefix(target, storage.KindMarkdown)
	kind, name, _ := splitTarget(target)
	if kind != "c2c" || name == "" {
		c.log.Warn("web send with bad target", "target", msg.Target)
		return nil
	}
	c.publish(name, WireMessage{
		ID:   msg.ID,
		Role: "agent",
		Name: "MineAgent",
		Text: msg.Text,
		At:   msg.CreatedAt,
	})
	return nil
}

// subscribe/unsubscribe：SSE 连接注册；publish 时非阻塞投递，慢客户端丢事件
// （前端会靠 /api/history 补齐，不阻塞 agent）。
func (c *Channel) subscribe(name string, ch chan []byte) {
	c.mu.Lock()
	set := c.subs[name]
	if set == nil {
		set = make(map[chan []byte]struct{})
		c.subs[name] = set
	}
	set[ch] = struct{}{}
	c.mu.Unlock()
}

func (c *Channel) unsubscribe(name string, ch chan []byte) {
	c.mu.Lock()
	if set := c.subs[name]; set != nil {
		delete(set, ch)
		if len(set) == 0 {
			delete(c.subs, name)
		}
	}
	c.mu.Unlock()
}

// publish 把一条消息送给该账号所有在线标签页。
func (c *Channel) publish(name string, msg WireMessage) {
	c.publishRaw(name, map[string]any{"type": "message", "message": msg})
}

// publishRaw 广播任意 SSE 事件（message / cleared）。
func (c *Channel) publishRaw(name string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.mu.Lock()
	set := c.subs[name]
	chans := make([]chan []byte, 0, len(set))
	for ch := range set {
		chans = append(chans, ch)
	}
	c.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- data:
		default:
		}
	}
}

// session 取/建账号会话并注册本通道（模式同其他通道）。
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

// HandleUserMessage 是一条来自网页的消息入口：系统命令直回 /
// 限流 / 入库 / 触发 agent。files 是本次上传的文件（可为空）。
func (c *Channel) HandleUserMessage(ctx context.Context, name, text string, files []UploadedFile) error {
	sessionKey := "web:c2c:" + name
	target := "c2c:" + name

	text = strings.TrimSpace(text)
	if text == "" && len(files) == 0 {
		return fmt.Errorf("消息为空")
	}

	// 限流：同一会话两次请求至少间隔 minInterval，防手抖刷屏烧 token。
	c.mu.Lock()
	last := c.lastSend[sessionKey]
	if time.Since(last) < time.Duration(c.cfg.MinIntervalMS)*time.Millisecond {
		c.mu.Unlock()
		return fmt.Errorf("慢一点，消息太密了")
	}
	c.mu.Unlock()

	sess, err := c.session(sessionKey)
	if err != nil {
		return err
	}

	// 系统命令硬编码直回（与其它通道一致），不进 agent 队列。
	if text != "" {
		if cmd, arg := tools.MatchSystemCommand(text); cmd != "" {
			reply := tools.ExecSystemCommand(tools.SysCtx{
				Ctx:       ctx,
				Store:     c.store,
				SessionID: sessionKey,
				Channel:   "web",
				IsAdmin:   IsAdminName(name, c.cfg.AdminUsers),
				Model:     c.model,
				MCStatus:  c.mcStatus,
				QQStatus:  c.WebStatus,
				QQIDs:     []string{name},
				Requester: "web:" + name,
			}, cmd, arg)
			c.log.Info("web syscmd", "session", sessionKey, "cmd", cmd, "arg", arg)
			return sess.Reply(ctx, reply, target)
		}
	}

	// 入库文本：用户原文（破坏伪造标记）+ 每个文件一行标记（agent 可见路径）。
	body := EscapeFileMarkers(text)
	for _, f := range files {
		if body != "" {
			body += "\n"
		}
		body += BuildFileMarker(f)
	}

	stored, err := sess.Ingest(ctx, storage.Message{
		Channel:    "web",
		AuthorKind: "player",
		AuthorID:   sessionKey,
		AuthorName: name,
		Text:       body,
		Target:     target,
	})
	if err != nil {
		return err
	}
	// 多标签页同步：本通道的 Ingest 不 fanout 给自己，这里手动广播。
	c.publish(name, ToWire(stored))

	now := time.Now().UnixMilli()
	_ = c.store.UpsertIdentity(ctx, "web", name, name, now)

	mcName := ""
	if bound, err := c.store.LinkedMC(ctx, tools.BindPlatform, name); err == nil {
		mcName = bound
	}

	c.log.Info("web trigger", "session", sessionKey, "author", name,
		"files", len(files), "boundMC", mcName, "query", text, "messageId", stored.ID)

	if !c.ag.Submit(agent.Request{
		Session:           sess,
		SessionKey:        sessionKey,
		Player:            "web:" + name,
		RequesterID:       name,
		MCRequester:       mcName,
		Query:             body,
		TriggerMessageID:  stored.ID,
		ReplyTarget:       target,
		Tools:             c.webTools,
		SystemInstruction: webInstruction,
	}) {
		_ = sess.Reply(ctx, "抱歉，我现在忙不过来了，稍后再试。", target)
	}

	c.mu.Lock()
	c.lastSend[sessionKey] = time.Now()
	c.mu.Unlock()
	return nil
}

// loadTokens / saveTokens：登录令牌落盘（0600），支持进程重启后浏览器不掉线。
// 新格式 name -> [token...]；兼容旧的 name -> token 单值格式。
func (c *Channel) loadTokens() {
	b, err := os.ReadFile(c.tokensPath)
	if err != nil {
		return
	}
	var m map[string][]string
	if err := json.Unmarshal(b, &m); err != nil {
		var old map[string]string
		if json.Unmarshal(b, &old) != nil {
			return
		}
		m = make(map[string][]string, len(old))
		for k, v := range old {
			m[k] = []string{v}
		}
	}
	for k, list := range m {
		if !ValidAccountName(k) {
			continue
		}
		for _, v := range list {
			if v != "" {
				c.tokens[k] = append(c.tokens[k], v)
			}
		}
	}
}

func (c *Channel) saveTokens() {
	c.mu.Lock()
	m := make(map[string][]string, len(c.tokens))
	for k, v := range c.tokens {
		m[k] = append([]string(nil), v...)
	}
	c.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(c.tokensPath), 0o700); err != nil {
		c.log.Warn("web tokens mkdir", "err", err)
		return
	}
	b, _ := json.Marshal(m)
	tmp := c.tokensPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		c.log.Warn("web tokens write", "err", err)
		return
	}
	if err := os.Rename(tmp, c.tokensPath); err != nil {
		c.log.Warn("web tokens rename", "err", err)
	}
}

// login 给账号发一个新令牌：多设备/多浏览器可并存（各自保留），
// 每个名字最多 5 个令牌，最旧的淘汰（防令牌文件无限膨胀）。
func (c *Channel) login(name string) (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	list := append(c.tokens[name], tok)
	if len(list) > 5 {
		list = list[len(list)-5:]
	}
	c.tokens[name] = list
	c.mu.Unlock()
	c.saveTokens()
	return tok, nil
}

// logout 注销单个令牌（前端"退出登录"），并持久化。
func (c *Channel) logout(tok string) {
	if tok == "" {
		return
	}
	c.mu.Lock()
	for name, list := range c.tokens {
		out := list[:0]
		for _, t := range list {
			if t != tok {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			delete(c.tokens, name)
		} else {
			c.tokens[name] = out
		}
	}
	c.mu.Unlock()
	c.saveTokens()
}

// nameByToken 反查令牌归属；不带令牌/令牌过期返回 ""。
func (c *Channel) nameByToken(tok string) string {
	if tok == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, list := range c.tokens {
		for _, t := range list {
			if t == tok {
				return name
			}
		}
	}
	return ""
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

const webInstruction = `你是 MineAgent，一个能写代码、执行代码、查资料、画图发文件的轻量 agent，
当前在网页聊天界面里跟人聊天。你的能力很多，Minecraft 服务器「jzk 的服务器」只是其中一个
可用功能（查服状态、传送/给物/执行命令需审批），不要把自己只当成服的客服。

规则：
- 用简体中文回答，语气轻松友好。
- 网页支持 Markdown 渲染（标题/列表/加粗/代码块/链接都行），单条 4000 字内；
  复杂内容可以直接排版，不用拆分。
- 用户上传的图片会直接附在消息里（你能看到图片内容），直接看直接答；
  不要再用 PIL 像素统计/ASCII 画之类的方式去"猜"图，那样又慢又不准，
  真看不到就说看不到。图片/文件也可以用 workspace 工具进一步处理（管理员可用）。
- 非图片文件会以 [[file:workspace相对路径|文件名|类型|大小]] 标记给出路径，
  需要时用 workspace 工具（workspace_read/exec，管理员可用）读取；不要编造文件内容。
- 发文件/图片：先把文件做到 workspace 里（png/jpg/gif/webp 图片会在网页内联显示），
  再调 web_file 发，path 写相对路径。发送失败会如实报错，不要编造"已发送"。
- 做多步任务（查数据->装包->画图->发文件）时：每步一次只调一个工具，拿到结果再调下一步；
  画图直接用 PIL（已装），中文字体用 /usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc（已装）。
  不要反复试探同一条失败命令，换一条路走；20 步内完不成就先回一条进度，再继续。
- MC 服务器只是功能之一：被问到服实时情况（在线玩家、TPS/内存、时间、天气）时才调用
  minecraft_* 只读工具查，不要编造；平时聊天、写代码、查资料都不用碰 MC。
- minecraft_teleport / minecraft_give / minecraft_run_command 是高权限操作：只能应明确请求发起，
  发起后必须等待游戏内管理员批准；请求者没有绑定 MC 身份时要先提醒他用「绑定 <MC名>」绑定。
- workspace_ls / workspace_read / workspace_write / workspace_exec 是写代码和执行代码的工具，
  只能管理员（web.adminUsers）使用——非管理员调用会被直接拒绝，你不要绕过。
  所有操作都被限制在 workspace 目录内；执行命令有超时和输出上限。
  装依赖用 $VENV_BIN/pip install（只能装进 workspace/.venv），下载用 curl/wget（只允许从公开 http(s) 下载到 workspace 内）；
  这两类会先过静态约束再送 LLM 语义审查，审查不通过就执行不了——被拒时如实转告，不要编造结果。
  写文件前先 ls/read 确认，不要覆盖已有重要文件；exec 一次只做一件事，重要操作先 dry-run。
- 对话管理命令（/help /status /memory /bind /unbind /myid）由系统层直接回复，
  不经过你：如果用户问起这些命令，你照着 help 文案介绍，不要自己编命令列表。
- 不确定的信息不要编造，直接说不知道。`
