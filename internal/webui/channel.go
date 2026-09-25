package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"mineagent/internal/account"
	"mineagent/internal/agent"
	"mineagent/internal/config"
	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
	"mineagent/internal/usage"
)

// Channel 是网页通道：浏览器 -> 会话隔离 -> agent -> SSE 推回网页。
//
// 会话隔离：每个账号一个会话 web:user:<用户ID>（用户ID 来自 account 包，
// 名字只是登录名，见 MINE_PORTAL_DESIGN.md §5）。
// 与 qq:/wecom:/minecraft- 会话天然隔离；同一会话多标签页共享消息（SSE 广播）。
//
// 收发文件：
//   - 用户上传：POST /api/upload 存到 workspace/web-files/u<用户ID>/ 下，
//     消息文本里带 [[file:...]] 标记（agent 可见路径，前端显示卡片）。
//   - agent 发文件：web_file 工具（tools.WebSend）+ webToolGate，
//     消息 Target 前缀 file:，网页端内联图片/下载链接。
//
// 安全：账号登录/令牌由 internal/account 统一负责（无密码，名字即账号），
// web.users 可配白名单；workspace/审批权限只给 web.adminUsers。
type Channel struct {
	log *slog.Logger
	cfg config.Web
	// workspaceRoot 上传目录/发文件都在它下面。
	workspaceRoot string
	model         string
	mcStatus      func(ctx context.Context) (string, error)
	usage         *usage.Client

	hub      *session.Hub
	store    *storage.Store
	acct     *account.Service
	ag       *agent.Agent
	webTools []tool.BaseTool

	// 模型切换：可选项（config.model.options 优先，否则拉网关）与加载器。
	modelOptions   []string
	models         *modelLister
	providers      []config.Provider
	providerModels map[string]providerCache
	modelBaseURL   string

	srv *http.Server

	mu       sync.Mutex
	sessions map[string]*session.Session
	lastSend map[string]time.Time
	// subs: 账号名 -> SSE 订阅者集合（用户名唯一且不变，用它做订阅键最省事）。
	subs map[string]map[chan []byte]struct{}
	// prefs: 账号名 -> 模型/思考强度偏好（+ 菜单里改），持久化。
	prefs     map[string]accountPrefs
	prefsPath string
	// running: 会话 key -> 当前运行的进度状态（内存态，重启即清）。
	running map[string]runState
}

func NewChannel(log *slog.Logger, cfg config.Config, acct *account.Service, hub *session.Hub, store *storage.Store,
	ag *agent.Agent, webTools []tool.BaseTool) *Channel {
	dataDir := cfg.Web.DataDir
	if dataDir == "" {
		dataDir = "data/webui"
	}
	c := &Channel{
		log:            log,
		cfg:            cfg.Web,
		workspaceRoot:  cfg.WorkspaceRoot(),
		model:          cfg.Model.Name,
		usage:          usage.New(cfg.Model.BaseURL, cfg.Model.APIKey),
		modelOptions:   cfg.Model.Options,
		providers:      cfg.Model.Providers,
		providerModels: make(map[string]providerCache),
		modelBaseURL:   cfg.Model.BaseURL,
		hub:            hub,
		store:          store,
		acct:           acct,
		ag:             ag,
		webTools:       webTools,
		sessions:       make(map[string]*session.Session),
		lastSend:       make(map[string]time.Time),
		subs:           make(map[string]map[chan []byte]struct{}),
		prefs:          make(map[string]accountPrefs),
		prefsPath:      filepath.Join(dataDir, "prefs.json"),
		running:        make(map[string]runState),
	}
	c.models = newModelLister(cfg.Model.BaseURL, cfg.Model.APIKey, dataDir, func(f string, a ...any) { log.Warn(fmt.Sprintf(f, a...)) })
	c.loadPrefs()
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

// Send 实现 session.Channel：agent 的回复 -> 对应账号/会话的 SSE 订阅者。
// Target="c2c:<名字>"（file: 前缀是 agent 发的文件消息），conv 从 SessionID 反解。
func (c *Channel) Send(ctx context.Context, msg storage.Message) error {
	target := strings.TrimPrefix(msg.Target, storage.KindFile)
	target = strings.TrimPrefix(target, storage.KindMarkdown)
	kind, name, _ := splitTarget(target)
	if kind != "c2c" || name == "" {
		c.log.Warn("web send with bad target", "target", msg.Target)
		return nil
	}
	conv := convOfSession(msg.SessionID)
	c.clearRunning(msg.SessionID)
	// 用 ToWire 而不是手拼：文件/图片消息（Target 前缀 file:）必须带上 files，
	// 否则网页端只有刷新走 history 才能看到附件（线上踩过）。
	c.publish(name, conv, ToWire(msg))
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

// publish 把一条消息送给该账号所有在线标签页（带会话 id，前端按当前会话过滤）。
func (c *Channel) publish(name, conv string, msg WireMessage) {
	c.publishRaw(name, map[string]any{"type": "message", "conv": conv, "message": msg})
}

// conversationTitle 首条消息自动命名：取第一行前 24 个字。
func conversationTitle(text string, files []UploadedFile) string {
	t := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	if t == "" && len(files) > 0 {
		t = "文件：" + files[0].Name
	}
	if t == "" {
		return "新会话"
	}
	r := []rune(t)
	if len(r) > 24 {
		return string(r[:24])
	}
	return t
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

// runState 是某会话正在运行的进度状态（切走再回来/刷新页面时恢复状态行用）。
type runState struct {
	Text      string `json:"text"`
	StartedAt int64  `json:"startedAt"`
}

// ErrConvNotFound 会话不存在（发送时给 404 而不是限流 429）。
var ErrConvNotFound = errors.New("会话不存在")

// webSessionKey 用户ID + 会话短 id -> 会话 key（conv 为空是默认会话）。
func webSessionKey(userID int64, conv string) string {
	base := "web:user:" + strconv.FormatInt(userID, 10)
	if conv == "" {
		return base
	}
	return base + ":" + conv
}

// convOfSession 从会话 key 反解会话短 id；兼容迁移前的 web:c2c:<名字> 形式。
func convOfSession(sessionID string) string {
	for _, prefix := range []string{"web:user:", "web:c2c:"} {
		if rest, ok := strings.CutPrefix(sessionID, prefix); ok {
			if i := strings.Index(rest, ":"); i >= 0 {
				return rest[i+1:]
			}
			return ""
		}
	}
	return ""
}

// sessionBelongsTo 判断会话 key 是否属于该用户（默认会话或带 conv 的）。
func sessionBelongsTo(sessionID string, userID int64) bool {
	prefix := "web:user:" + strconv.FormatInt(userID, 10)
	if !strings.HasPrefix(sessionID, prefix) {
		return false
	}
	rest := sessionID[len(prefix):]
	return rest == "" || (strings.HasPrefix(rest, ":") && len(rest) > 1)
}

// ValidConv 校验会话短 id（客户端只从服务端拿，格式固定）。
func ValidConv(conv string) bool {
	if conv == "" {
		return true
	}
	if len(conv) < 6 || len(conv) > 32 {
		return false
	}
	for _, r := range conv {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// session 取/建账号会话并注册本通道（模式同其他通道）。
// setRunning 记录/更新某会话的运行状态（同一 run 保留首次的开始时间）。
func (c *Channel) setRunning(sessionKey, text string) {
	c.mu.Lock()
	st := c.running[sessionKey]
	if st.StartedAt == 0 {
		st.StartedAt = time.Now().UnixMilli()
	}
	st.Text = text
	c.running[sessionKey] = st
	c.mu.Unlock()
}

func (c *Channel) clearRunning(sessionKey string) {
	c.mu.Lock()
	delete(c.running, sessionKey)
	c.mu.Unlock()
}

// RunningState 取运行状态；超过 15 分钟视为过期（防止进程内残留）。
func (c *Channel) RunningState(sessionKey string) (runState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.running[sessionKey]
	if !ok {
		return runState{}, false
	}
	if time.Since(time.UnixMilli(st.StartedAt)) > 15*time.Minute {
		delete(c.running, sessionKey)
		return runState{}, false
	}
	return st, true
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

// HandleUserMessage 是一条来自网页的消息入口：系统命令直回 /
// 限流 / 入库 / 触发 agent。
//
// conv 语义（对应用户端的 Draft 状态机）：
//   - nil  = 新会话草稿：这里才真正创建会话行（第一条消息原子创建），返回新 id；
//   - ""   = 旧版默认会话（首次发送时补落会话行）；
//   - "id" = 已存在会话，不存在返回 ErrConvNotFound。
//
// files 是本次上传的文件。返回该消息所属的会话 id。
func (c *Channel) HandleUserMessage(ctx context.Context, u *storage.User, conv *string, text string, files []UploadedFile) (string, error) {
	name := u.Username
	convID := ""
	isNew := conv == nil
	if !isNew {
		convID = *conv
	}
	sessionKey := webSessionKey(u.ID, convID)
	target := "c2c:" + name
	nowMs := time.Now().UnixMilli()

	text = strings.TrimSpace(text)
	if text == "" && len(files) == 0 {
		return convID, fmt.Errorf("消息为空")
	}

	// 限流：同一会话两次请求至少间隔 minInterval，防手抖刷屏烧 token。
	c.mu.Lock()
	last := c.lastSend[sessionKey]
	if time.Since(last) < time.Duration(c.cfg.MinIntervalMS)*time.Millisecond {
		c.mu.Unlock()
		return convID, fmt.Errorf("慢一点，消息太密了")
	}
	c.mu.Unlock()

	// 会话落库：新会话在这里创建（第一条消息才产生记录）；旧默认会话补行；已有会话校验存在。
	if isNew {
		id, err := randomConv()
		if err != nil {
			return "", fmt.Errorf("生成会话失败")
		}
		convID = id
		sessionKey = webSessionKey(u.ID, convID)
		if err := c.store.UpsertConversationFor(ctx, u.ID, name, convID, "", nowMs); err != nil {
			return "", err
		}
		c.log.Info("web conversation created", "user", name, "uid", u.ID, "conv", convID)
	} else if convID == "" {
		_ = c.store.UpsertConversationFor(ctx, u.ID, name, "", "", nowMs)
	} else if existing, err := c.store.ConversationFor(ctx, u.ID, name, convID); err != nil {
		return "", err
	} else if existing == nil {
		return "", ErrConvNotFound
	}

	// 首条消息自动命名（系统命令路径也要，否则会话列表/首页卡片没有标题）。
	if isNew || convID == "" {
		_ = c.store.SetConversationTitleIfEmptyFor(ctx, u.ID, name, convID, conversationTitle(text, files), nowMs)
	}

	sess, err := c.session(sessionKey)
	if err != nil {
		return convID, err
	}

	// 系统命令硬编码直回（与其它通道一致），不进 agent 队列。
	// 先把用户这条命令入库/广播，聊天里才能看到自己发了什么。
	if text != "" {
		if cmd, arg := tools.MatchSystemCommand(text); cmd != "" {
			if stored, err := sess.Ingest(ctx, storage.Message{
				Channel:    "web",
				AuthorKind: "player",
				AuthorID:   sessionKey,
				AuthorName: name,
				Text:       text,
				Target:     target,
			}); err == nil {
				c.publish(name, convID, ToWire(stored))
			}
			reply := tools.ExecSystemCommand(tools.SysCtx{
				Ctx:       ctx,
				Store:     c.store,
				SessionID: sessionKey,
				Channel:   "web",
				IsAdmin:   c.acct.IsAdminName(name),
				Model:     c.model,
				Usage:     c.usage.Text,
				MCStatus:  c.mcStatus,
				QQStatus:  c.WebStatus,
				QQIDs:     []string{name},
				Requester: "web:" + name,
			}, cmd, arg)
			c.log.Info("web syscmd", "session", sessionKey, "cmd", cmd, "arg", arg)
			return convID, sess.Reply(ctx, reply, target)
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
		return convID, err
	}
	// 刷新会话时间；多标签页同步广播（带 conv）。
	_ = c.store.UpsertConversationFor(ctx, u.ID, name, convID, "", nowMs)
	c.publish(name, convID, ToWire(stored))

	now := time.Now().UnixMilli()
	_ = c.store.UpsertIdentity(ctx, "web", name, name, now)

	mcName := ""
	if bound, err := c.store.LinkedMC(ctx, tools.BindPlatform, name); err == nil {
		mcName = bound
	}

	c.log.Info("web trigger", "session", sessionKey, "author", name,
		"files", len(files), "boundMC", mcName, "query", text, "messageId", stored.ID)

	prefs := c.prefsOf(name)
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
		Model:             prefs.Model,
		ReasoningEffort:   prefs.Effort,
		Progress: func(text string) {
			c.setRunning(sessionKey, text)
			c.publishRaw(name, map[string]any{"type": "progress", "conv": convID, "text": text})
		},
	}) {
		_ = sess.Reply(ctx, "抱歉，我现在忙不过来了，稍后再试。", target)
	}

	c.mu.Lock()
	c.lastSend[sessionKey] = time.Now()
	c.mu.Unlock()
	return convID, nil
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

const webInstruction = `你是 MineAgent，一个能动手的 agent（写代码/执行、联网搜索、收发文件、画图、设提醒），
当前在网页聊天界面里跟人聊天，支持 Markdown 排版、文件与图片收发。
Minecraft 服务器「jzk 的服务器」只是你能做的一件事（查状态、传送/给物/执行命令需审批），
不要把自己只当成服的客服。
` +
	agent.CoreAgentPrinciples + `
渠道规则（网页）：
- 用简体中文回答，语气轻松友好；网页支持 Markdown（标题/列表/加粗/链接/表格/任务清单）
  与代码块语法高亮；**数学公式用 LaTeX**：行内 $...$、独立成行 $$...$$（会被渲染），
  单条 4000 字内，复杂内容可以直接排版。
- 用户上传的图片会**直接附在消息里**（你能看到图片内容），直接看直接答；
  不要用 PIL 像素统计/ASCII 画之类的方式去"猜"图，真看不到就说看不到。
  非图片文件以 [[file:workspace相对路径|文件名|类型|大小]] 标记给出路径，
  需要时用 workspace 工具读取（管理员），不要编造文件内容。
- 发文件/图片：先把文件做到 workspace 里（png/jpg/gif/webp 会内联显示），
  再调 web_file 发，path 写相对路径；发送失败会如实报错。
- 画图直接用 PIL（已装），中文字体用 /usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc（已装）。
  多步任务一次只调一个工具，不要反复试探同一条失败命令，20 步内完不成就先回一条进度。
- MC 服务器只是功能之一：只在被问到服实时情况时才调 minecraft_* 工具。
- minecraft_teleport / minecraft_give / minecraft_run_command 只能应明确请求发起，
  发起后等游戏内管理员批准；请求者没绑定 MC 身份时先提醒他用「绑定 <MC名>」。
- workspace_* 仅管理员（web.adminUsers）可用，非管理员调用会被直接拒绝。
  装依赖用 $VENV_BIN/pip install，下载用 curl/wget（仅公开 http(s) 到 workspace），
  这两类先过静态约束再送 LLM 语义审查，被拒时如实转告。
- 对话管理命令（/help /status /memory /usage /bind /unbind /myid）由系统层直接回复，
  你照着 help 文案介绍，不要自己编命令列表。`
