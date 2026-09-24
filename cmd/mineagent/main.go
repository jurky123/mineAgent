package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"mineagent/internal/agent"
	"mineagent/internal/aibot"
	"mineagent/internal/channels/minecraft"
	"mineagent/internal/config"
	"mineagent/internal/protocol"
	"mineagent/internal/qq"
	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
	"mineagent/internal/version"
	"mineagent/internal/webui"
	"mineagent/internal/wechat"
	"mineagent/internal/wecom"
	"mineagent/internal/ws"
)

func main() {
	configPath := flag.String("config", "config.json", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	wechatLogin := flag.Bool("wechat-login", false, "扫码登录个人微信 ClawBot（登录后退出）")
	flag.Parse()

	if *showVersion {
		fmt.Printf("mineagent %s (protocol v%d)\n", version.Version, protocol.Version)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	// 扫码登录：独立流程，登完退出，由 systemd 常驻进程使用登录后的凭证。
	if *wechatLogin {
		log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
		statePath := wechat.StatePathFor(cfg.Storage.Path)
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
		defer cancel()
		state, err := wechat.Login(ctx, log, "/tmp/opencode/wechat-qr.png")
		if err != nil {
			fmt.Fprintf(os.Stderr, "wechat login failed: %v\n", err)
			os.Exit(1)
		}
		if err := wechat.SaveState(statePath, state); err != nil {
			fmt.Fprintf(os.Stderr, "save state failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("登录成功，凭证已保存到 %s\nilink_user_id=%s\n", statePath, state.ILinkUserID)
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.SlogLevel()}))
	log.Info("starting mineagent", "version", version.Version, "protocol", protocol.Version, "config", *configPath)

	store, err := storage.Open(cfg.Storage.Path)
	if err != nil {
		log.Error("open storage", "err", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if n, err := store.ClearCheckpoints(ctx); err != nil {
		log.Warn("clear stale checkpoints failed", "err", err)
	} else if n > 0 {
		log.Info("cleared stale checkpoints", "count", n)
	}

	hub := session.NewHub(store, log)
	sess, err := hub.Session(ctx, cfg.Minecraft.SessionID)
	if err != nil {
		log.Error("open session", "err", err)
		os.Exit(1)
	}
	mc := minecraft.NewChannel(log)
	sess.Register(mc)

	gw := tools.NewGateway(mc, log)
	approvalTimeout := time.Duration(cfg.Tools.ApprovalTimeoutSeconds) * time.Second
	approvals := tools.NewApprovals(mc, approvalTimeout, log)
	approvals.SetMaxPerRequester(cfg.QQ.MaxPendingPerUser)
	agentTools := append(tools.ReadOnly(gw), tools.Privileged(gw, approvals, store, log)...)

	// MC 通道的查服状态闭包（/status 直回用，经网关调 minecraft_server_status）。
	mcStatusFn := func(ctx context.Context) (string, error) {
		return gw.Call(ctx, "minecraft_server_status", json.RawMessage(`{}`))
	}

	// QQ 通道工具集：只读查询 + MC 高权限（转游戏内审批）+ workspace 四件套 + 绑定。
	// 注意 workspace_exec 的管理员门禁不在工具里写死，而是在 Submit 前按消息身份
	// 动态放进 ctx（WithQQAdmin），这样同一套工具对管理员/普通群友表现不同。
	// qq_bind/qq_unbind 的身份同样走 ctx（WithQQIdentity）。
	// workspace 的管理员名单：四个 IM 通道的 admin 列表取并集
	//（同一个人在不同通道有不同 ID：QQ openid / 企微 userid / 网页名字）。
	adminIDs := append(append(append([]string{}, cfg.QQ.AdminOpenIDs...), cfg.WeCom.AdminUserIDs...), cfg.AIBot.AdminUserIDs...)
	adminIDs = append(adminIDs, cfg.Web.AdminUsers...)
	wsTools := tools.NewWorkspace(cfg.Workspace, adminIDs, store, log)
	// workspace_exec 的 LLM 二审：默认复用主模型（省一个配置），
	// 想用更便宜/更严的模型就填 workspace.review.baseURL/apiKey/model。
	// enabled=false 或主模型都没配 = 无审查器，review 类命令 fail-closed 全拒。
	// 注意 config.json 里没有 review 段时 Enabled 默认为 false（零值），
	// 但老版本是"配了主模型就默认开"——为保持线上行为，这里只要主模型配了就开，
	// 除非显式 disabled（见 config.EffectiveReviewEnabled）。
	if cfg.EffectiveReviewEnabled() {
		baseURL := cfg.Workspace.Review.BaseURL
		apiKey := cfg.Workspace.Review.APIKey
		model := cfg.Workspace.Review.Model
		if baseURL == "" {
			baseURL = cfg.Model.BaseURL
		}
		if apiKey == "" {
			apiKey = cfg.Model.APIKey
		}
		if model == "" {
			model = cfg.Model.Name
		}
		wsTools.SetReviewer(tools.NewLLMReviewer(baseURL, apiKey, model,
			time.Duration(cfg.Workspace.Review.TimeoutSec)*time.Second, log).
			WithSessionID("mineagent-reviewer"))
		log.Info("workspace reviewer enabled", "model", model)
	} else {
		log.Warn("workspace reviewer disabled, review-gated commands will be denied",
			"hint", "set workspace.review.enabled=true and model.baseURL/model.name")
	}
	bindTools := tools.NewQQBind(store, log)
	sendTools := tools.NewQQSend()
	wecomSendTools := tools.NewWeComSend()
	webSendTools := tools.NewWebSend()
	qqBaseTools := append(append(tools.ReadOnly(gw), tools.Privileged(gw, approvals, store, log)...),
		append(append(wsTools.Tools(), bindTools.Tools()...), sendTools.Tools()...)...)
	wecomBaseTools := append(append(tools.ReadOnly(gw), tools.Privileged(gw, approvals, store, log)...),
		append(append(wsTools.Tools(), bindTools.Tools()...), wecomSendTools.Tools()...)...)
	webBaseTools := append(append(tools.ReadOnly(gw), tools.Privileged(gw, approvals, store, log)...),
		append(append(wsTools.Tools(), bindTools.Tools()...), webSendTools.Tools()...)...)

	ag, err := agent.New(ctx, cfg, store, log, agentTools, approvals)
	if err != nil {
		log.Error("init agent", "err", err)
		os.Exit(1)
	}
	if ag.Enabled() {
		go ag.Run(ctx)
	}

	// QQ 通道：配了 appId+secret 才启动，否则完全不影响 MC 链路。
	sessionsFn := func(ctx context.Context, sessionKey string) (*session.Session, error) {
		return hub.Session(ctx, sessionKey)
	}
	senderFn := func(ctx context.Context, sess *session.Session, target, text string) error {
		return sess.Reply(ctx, text, target)
	}
	var qqCh *qq.Channel
	if cfg.QQ.AppID != "" && cfg.QQ.Secret != "" {
		tokens := qq.NewTokenSource(cfg.QQ.AppID, cfg.QQ.Secret, cfg.QQ.APIBase, log)
		api := qq.NewAPI(cfg.QQ.APIBase, tokens, log)
		qqCh = qq.NewChannel(log, cfg, hub, store, ag, api, wrapQQTools(qqBaseTools, wsTools, store, sessionsFn, senderFn)).
			WithMCStatus(mcStatusFn)
		qqCh.Start()
		defer qqCh.Stop()
		log.Info("qq channel enabled")
	} else {
		log.Info("qq channel disabled (qq.appId/appSecret empty)")
	}

	// 企业微信通道：secret/token/aesKey 配齐就启动（corpId 可留空，
	// 首次回调解密时会自动学到并写日志，之后补进 config.json 更稳）。
	// 回调服务监听 cfg.WeCom.Port（默认 80，微信只允许 80/443），
	// 腾讯云控制台需放行该端口；回复走主动 SendText，无 5 秒窗口压力。
	var wecomCh *wecom.Channel
	if cfg.WeCom.Secret != "" && cfg.WeCom.Token != "" && cfg.WeCom.EncodingAES != "" {
		wecomTokens := wecom.NewTokenSource(cfg.WeCom.CorpID, cfg.WeCom.Secret, log)
		wecomAPI := wecom.NewAPI(wecomTokens, cfg.WeCom.AgentID, log)
		wecomCh = wecom.NewChannel(log, cfg, hub, store, ag, wecomAPI,
			wrapWeComTools(wecomBaseTools, wsTools, store, sessionsFn, senderFn)).
			WithMCStatus(mcStatusFn)
		go func() {
			if err := wecomCh.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("wecom callback stopped", "err", err)
			}
		}()
		defer wecomCh.Stop()
		log.Info("wecom channel enabled", "port", cfg.WeCom.Port, "corpIdKnown", cfg.WeCom.CorpID != "")
	} else {
		log.Info("wecom channel disabled (wecom.secret/token/encodingAesKey incomplete)")
	}

	// 企微智能机器人（长连接）：botId+secret 配齐就启动。
	// 无需公网回调、无需加解密；扫码可加为联系人、可进内部群被 @。
	var aibotCh *aibot.Channel
	if cfg.AIBot.BotID != "" && cfg.AIBot.Secret != "" {
		// aibot v1 不带 wecom_* 发送工具（回复统一走 consume 的被动回复），
		// 复用 wecom 的门禁包装（识别 wecom: 前缀做 admin/绑定/审批）。
		aibotTools := append(append(tools.ReadOnly(gw), tools.Privileged(gw, approvals, store, log)...),
			append(wsTools.Tools(), bindTools.Tools()...)...)
		aibotCh = aibot.NewChannel(log, cfg, hub, store, ag,
			wrapWeComTools(aibotTools, wsTools, store, sessionsFn, senderFn)).
			WithMCStatus(mcStatusFn)
		aibotCh.Start()
		defer aibotCh.Stop()
		log.Info("aibot channel enabled", "botId", cfg.AIBot.BotID)
	} else {
		log.Info("aibot channel disabled (aibot.botId/secret empty)")
	}

	// 个人微信 ClawBot（直连腾讯 iLink，不跑 OpenClaw）：扫码登录过才启用。
	var wechatCh *wechat.Channel
	wechatStatePath := wechat.StatePathFor(cfg.Storage.Path)
	wechatTools := append(append(tools.ReadOnly(gw), tools.Privileged(gw, approvals, store, log)...),
		append(wsTools.Tools(), bindTools.Tools()...)...)
	wechatCh, err = wechat.NewChannel(log, cfg, wechatStatePath, hub, store, ag,
		wrapWeComTools(wechatTools, wsTools, store, sessionsFn, senderFn))
	if err != nil {
		log.Error("init wechat channel", "err", err)
		wechatCh = nil
	}
	if wechatCh != nil {
		wechatCh.WithMCStatus(mcStatusFn)
		wechatCh.Start()
		defer wechatCh.Stop()
		log.Info("wechat channel enabled", "state", wechatStatePath)
	} else if err == nil {
		log.Info("wechat channel disabled (未登录：跑 mineagent --wechat-login 扫码)", "state", wechatStatePath)
	}

	// 网页入口：web.listen 非空即启用（默认 0.0.0.0:8766，公网访问要在
	// 腾讯云控制台放行端口）。账号只用一个名字区分（无密码），
	// 支持收发图片/文件，会话隔离 web:c2c:<名字>，页面允许 iframe 嵌入。
	var webCh *webui.Channel
	if cfg.Web.Listen != "" {
		webCh = webui.NewChannel(log, cfg, hub, store, ag,
			wrapWebTools(webBaseTools, wsTools, store, sessionsFn, senderFn)).
			WithMCStatus(mcStatusFn)
		go func() {
			if err := webCh.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("web ui stopped", "err", err)
			}
		}()
		defer webCh.Stop()
		log.Info("webui channel enabled", "listen", cfg.Web.Listen, "url", "http://<公网IP>:"+webPort(cfg.Web.Listen))
	} else {
		log.Info("webui channel disabled (web.listen empty)")
	}

	handler := func(ctx context.Context, c *ws.Conn, env *protocol.Envelope) {
		if !authorizeMessage(c.RemoteRole(), env.Type, mc.Attached() == c) {
			log.Warn("message rejected",
				"role", c.RemoteRole(),
				"type", env.Type,
				"remote", c.Remote(),
				"attached", mc.Attached() == c,
			)
			return
		}
		switch env.Type {
		case protocol.TypeChatMessage:
			var chat protocol.ChatMessage
			if err := env.Decode(&chat); err != nil {
				log.Warn("bad chat.message", "err", err)
				return
			}
			msg, err := sess.Ingest(ctx, storage.Message{
				Channel:    "minecraft",
				AuthorKind: "player",
				AuthorID:   chat.UUID,
				AuthorName: chat.Player,
				Text:       chat.Message,
			})
			if err != nil {
				log.Error("ingest chat", "err", err)
				return
			}
			if query, ok := matchTrigger(chat.Message, cfg.Minecraft.Trigger); ok {
				// 系统命令硬编码直回：不进 agent 队列，不调 LLM，直接回服内。
				// MC 通道 Target 语义：broadcast 模式传 ""（全体可见），player 模式传玩家名。
				if cmd, arg := tools.MatchSystemCommand(query); cmd != "" {
					target := chat.Player
					if cfg.Minecraft.ReplyMode == "broadcast" {
						target = ""
					}
					reply := tools.ExecSystemCommand(tools.SysCtx{
						Ctx:       ctx,
						Store:     store,
						SessionID: cfg.Minecraft.SessionID,
						Channel:   "minecraft",
						IsAdmin:   false,
						Model:     cfg.Model.Name,
						MCStatus:  mcStatusFn,
						QQIDs:     nil,
						Requester: chat.Player,
					}, cmd, arg)
					log.Info("mc syscmd", "player", chat.Player, "cmd", cmd, "arg", arg)
					_ = sess.Reply(ctx, reply, target)
					return
				}
				log.Info("agent trigger", "player", chat.Player, "query", query, "messageId", msg.ID)
				if !ag.Submit(agent.Request{
					Session:          sess,
					Player:           chat.Player,
					RequesterID:      chat.UUID,
					Query:            query,
					TriggerMessageID: msg.ID,
				}) {
					_ = sess.Reply(ctx, "MineAgent: 模型未配置，请在 config.json 填写 model.baseURL/name，Key 可放 MINEAGENT_MODEL_API_KEY", chat.Player)
				}
			}
		case protocol.TypeToolResult:
			var res protocol.ToolResult
			if err := env.Decode(&res); err != nil {
				log.Warn("bad tool.result", "err", err)
				return
			}
			gw.HandleResult(res)
		case protocol.TypeApprovalResult:
			var res protocol.ApprovalResult
			if err := env.Decode(&res); err != nil {
				log.Warn("bad approval.result", "err", err)
				return
			}
			approvals.HandleResult(res)
		default:
			log.Info("message", "role", c.RemoteRole(), "type", env.Type, "bytes", len(env.Data))
		}
	}

	srv := ws.New(cfg, log, handler)
	srv.OnConnect(func(c *ws.Conn) {
		if c.RemoteRole() == protocol.RoleMinecraft {
			mc.Attach(c)
		}
	})
	srv.OnDisconnect(func(c *ws.Conn) {
		if c.RemoteRole() == protocol.RoleMinecraft {
			mc.Detach(c)
		}
	})

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		if err != nil {
			log.Error("server stopped", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown", "err", err)
		}
	}
}

var minecraftMessageTypes = map[string]bool{
	protocol.TypeChatMessage:    true,
	protocol.TypeToolResult:     true,
	protocol.TypeApprovalResult: true,
}

// wrapQQTools 给 QQ 通道包一层身份门禁：每个工具调用前按 ctx 里的 QQ 身份决定
// workspace 管理员标记和绑定身份。MC 通道走原始工具，不受影响。
// ctx 里已有（agent.respond 放的）：requester=qq:<openid>、requesterID、
// session=qq:...、replyTarget、historyCutoff；这里追加：
//   - WithQQAdmin：union/user/member 任一命中 adminOpenIDs 即管理员。
//   - WithQQIdentity：openid 候选列表，供 qq_bind/qq_unbind 用。
//   - WithMCRequester：绑定的 MC 名，供 Gateway.Call 发给插件（requester 字段）。
//
// qq_markdown/qq_image 的发送经 sender/sessions 回调走 session fanout。
func wrapQQTools(base []tool.BaseTool, wsTools *tools.Workspace, store *storage.Store,
	sessions func(ctx context.Context, sessionKey string) (*session.Session, error),
	sender func(ctx context.Context, sess *session.Session, target, text string) error) []tool.BaseTool {
	out := make([]tool.BaseTool, 0, len(base))
	for _, tl := range base {
		out = append(out, &qqToolGate{inner: tl, ws: wsTools, store: store, sessions: sessions, sender: sender})
	}
	return out
}

// wrapWeComTools 与 wrapQQTools 同构，只是识别 wecom: 前缀与 wecom_* 发送工具。
func wrapWeComTools(base []tool.BaseTool, wsTools *tools.Workspace, store *storage.Store,
	sessions func(ctx context.Context, sessionKey string) (*session.Session, error),
	sender func(ctx context.Context, sess *session.Session, target, text string) error) []tool.BaseTool {
	out := make([]tool.BaseTool, 0, len(base))
	for _, tl := range base {
		out = append(out, &wecomToolGate{inner: tl, ws: wsTools, store: store, sessions: sessions, sender: sender})
	}
	return out
}

// wrapWebTools 与 wrapWeComTools 同构，识别 web: 前缀与 web_file 发送工具。
func wrapWebTools(base []tool.BaseTool, wsTools *tools.Workspace, store *storage.Store,
	sessions func(ctx context.Context, sessionKey string) (*session.Session, error),
	sender func(ctx context.Context, sess *session.Session, target, text string) error) []tool.BaseTool {
	out := make([]tool.BaseTool, 0, len(base))
	for _, tl := range base {
		out = append(out, &webToolGate{inner: tl, ws: wsTools, store: store, sessions: sessions, sender: sender})
	}
	return out
}

// webPort 从监听地址里抠端口，仅用于启动日志展示。
func webPort(listen string) string {
	if i := strings.LastIndex(listen, ":"); i >= 0 {
		return listen[i+1:]
	}
	return listen
}

func authorizeMessage(role, msgType string, attached bool) bool {
	if role != protocol.RoleMinecraft {
		return false
	}
	if !minecraftMessageTypes[msgType] {
		return false
	}
	return attached
}

func matchTrigger(text, trigger string) (string, bool) {
	if trigger == "" {
		return "", false
	}
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < len(trigger) || !strings.EqualFold(trimmed[:len(trigger)], trigger) {
		return "", false
	}
	return strings.TrimSpace(trimmed[len(trigger):]), true
}
