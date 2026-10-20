package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"mineagent/internal/account"
	"mineagent/internal/agent"
	"mineagent/internal/aibot"
	"mineagent/internal/body"
	"mineagent/internal/bodyui"
	"mineagent/internal/channels/minecraft"
	"mineagent/internal/commentary"
	"mineagent/internal/config"
	"mineagent/internal/games"
	"mineagent/internal/portal"
	"mineagent/internal/prompts"
	"mineagent/internal/protocol"
	"mineagent/internal/qq"
	"mineagent/internal/reminder"
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
	migratePortal := flag.Bool("migrate-portal", false, "把旧版 web:c2c:<名字> 数据迁到 Portal user_id 体系（先备份，幂等）")
	authResetPassword := flag.String("auth-reset-password", "", "operator-only password reset after verifying ownership")
	authInvite := flag.String("auth-invite", "", "operator verified account to activate (or recover)")
	authRecovery := flag.Bool("auth-recovery", false, "replace lost Passkeys after verifying account ownership")
	authFile := flag.String("auth-invite-file", "", "new 0600 file for the one-time activation secret")
	flag.Parse()
	if *authRecovery && *authInvite == "" {
		fmt.Fprintln(os.Stderr, "--auth-recovery requires --auth-invite")
		os.Exit(1)
	}

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

	// Portal 数据迁移：离线跑（先停 mineagent.service），先备份后改键。
	if *migratePortal {
		if err := runPortalMigration(ctx, log, cfg, store); err != nil {
			log.Error("portal migration failed", "err", err)
			os.Exit(1)
		}
		return
	}

	if *authResetPassword != "" {
		if *authInvite != "" {
			log.Error("choose only one authentication operation")
			os.Exit(1)
		}
		acct := account.New(store, log, cfg.Web.Users, cfg.Web.AdminUsers)
		if err := acct.ResetPassword(ctx, *authResetPassword); err != nil {
			log.Error("password reset failed", "err", err)
			os.Exit(1)
		}
		log.Info("password removed and sessions revoked; user must create a password at next login")
		return
	}
	if *authInvite != "" {
		if *authFile == "" {
			log.Error("--auth-invite-file is required; activation secrets are never logged")
			os.Exit(1)
		}
		output, err := os.OpenFile(*authFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			log.Error("cannot create activation file")
			os.Exit(1)
		}
		acct := account.New(store, log, cfg.Web.Users, cfg.Web.AdminUsers)
		code, err := acct.IssueEnrollment(ctx, *authInvite, *authRecovery)
		if err != nil {
			_ = output.Close()
			_ = os.Remove(*authFile)
			log.Error("cannot issue activation", "err", err)
			os.Exit(1)
		}
		_, writeErr := output.WriteString(code + "\n")
		closeErr := output.Close()
		if writeErr != nil || closeErr != nil {
			log.Error("cannot save activation file")
			os.Exit(1)
		}
		log.Info("activation saved; verify recipient ownership before delivering; expires in 24 hours", "file", *authFile)
		return
	}

	if n, err := store.ClearCheckpoints(ctx); err != nil {
		log.Warn("clear stale checkpoints failed", "err", err)
	} else if n > 0 {
		log.Info("cleared stale checkpoints", "count", n)
	}

	// 系统提示词覆盖：默认常量注册进 prompts，管理员可在「我的」页全局替换、用户可追加。
	dataDir := cfg.Web.DataDir
	if dataDir == "" {
		dataDir = "data/webui"
	}
	pm, err := prompts.Init(filepath.Join(dataDir, "prompts.json"))
	if err != nil {
		log.Warn("load prompts override", "err", err)
	}
	pm.Register(prompts.Service{ID: "web", Name: "网页聊天", Desc: "门户 /agent 网页对话", DefaultText: webui.DefaultCapabilities})
	pm.Register(prompts.Service{ID: "qq", Name: "QQ 机器人", Desc: "QQ 官方机器人", DefaultText: qq.DefaultCapabilities})
	pm.Register(prompts.Service{ID: "wecom", Name: "企业微信", Desc: "企微应用回调", DefaultText: wecom.DefaultCapabilities})
	pm.Register(prompts.Service{ID: "aibot", Name: "企微智能机器人", Desc: "企微长连接机器人", DefaultText: aibot.DefaultCapabilities})
	pm.Register(prompts.Service{ID: "wechat", Name: "个人微信", Desc: "ClawBot 直连", DefaultText: wechat.DefaultCapabilities})
	pm.Register(prompts.Service{ID: "minecraft", Name: "Minecraft", Desc: "服内聊天助手", DefaultText: agent.DefaultCapabilities})

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
	bodySvc := body.New(cfg.Body, store, approvals, log)
	bodySvc.WithWorldVerifier(func(ctx context.Context, sp protocol.BodySpec) error {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		raw, err := gw.Call(cctx, "minecraft_server_status", json.RawMessage(`{}`))
		if err != nil {
			return fmt.Errorf("world_unknown")
		}
		var server struct {
			Version string `json:"version"`
		}
		if json.Unmarshal([]byte(raw), &server) != nil || server.Version != cfg.Body.ExpectedMcVersion {
			return fmt.Errorf("unsupported")
		}
		args, _ := json.Marshal(map[string]string{"player": cfg.Body.Name})
		raw, err = gw.Call(cctx, "minecraft_player_info", args)
		if err != nil {
			return fmt.Errorf("world_unknown")
		}
		var player struct {
			Name    string `json:"name"`
			World   string `json:"world"`
			X, Y, Z float64
			OP      bool `json:"op"`
		}
		if json.Unmarshal([]byte(raw), &player) != nil || player.Name != cfg.Body.Name || player.World != sp.WorldID || player.OP {
			return fmt.Errorf("world_unknown")
		}
		actual := [3]float64{player.X, player.Y, player.Z}
		for i, v := range actual {
			if math.Abs(v-sp.Pos[i]) > 1.5 {
				return fmt.Errorf("stale")
			}
		}
		return nil
	})
	if bodySvc.Enabled() {
		go bodySvc.Run(ctx)
	}
	remindTools := tools.NewRemind(store, log).Tools()
	agentTools := append(append(tools.ReadOnly(gw), tools.Privileged(gw, approvals, store, log)...), remindTools...)

	// MC 通道的查服状态闭包（/status 直回用，经网关调 minecraft_server_status）。
	mcStatusFn := func(ctx context.Context) (string, error) {
		return gw.Call(ctx, "minecraft_server_status", json.RawMessage(`{}`))
	}

	// QQ 通道工具集：只读查询 + MC 高权限（转游戏内审批）+ workspace 四件套 + 绑定。
	// 注意 workspace_exec 的管理员门禁不在工具里写死，而是在 Submit 前按消息身份
	// 动态放进 ctx（WithQQAdmin），这样同一套工具对管理员/普通群友表现不同。
	// qq_bind/qq_unbind 的身份同样走 ctx（WithQQIdentity）。
	// ID 的命名空间属于各通道：跨通道同名不能继承管理员权限。
	wsTools := tools.NewWorkspace(cfg.Workspace, map[string][]string{
		"qq": cfg.QQ.AdminOpenIDs, "wecom": cfg.WeCom.AdminUserIDs,
		"aibot": cfg.AIBot.AdminUserIDs, "wechat": cfg.WeChat.AdminUserIDs,
		"web": cfg.Web.AdminUsers,
	}, store, log)
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
		append(append(append(wsTools.Tools(), bindTools.Tools()...), sendTools.Tools()...), remindTools...)...)
	wecomBaseTools := append(append(tools.ReadOnly(gw), tools.Privileged(gw, approvals, store, log)...),
		append(append(append(wsTools.Tools(), bindTools.Tools()...), wecomSendTools.Tools()...), remindTools...)...)
	webBaseTools := append(append(tools.ReadOnly(gw), tools.Privileged(gw, approvals, store, log)...),
		append(append(append(wsTools.Tools(), bindTools.Tools()...), webSendTools.Tools()...), remindTools...)...)

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
			wrapWeComTools("wecom", wecomBaseTools, wsTools, store, sessionsFn, senderFn)).
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
			append(append(wsTools.Tools(), bindTools.Tools()...), remindTools...)...)
		aibotCh = aibot.NewChannel(log, cfg, hub, store, ag,
			wrapWeComTools("aibot", aibotTools, wsTools, store, sessionsFn, senderFn)).
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
		append(append(wsTools.Tools(), bindTools.Tools()...), remindTools...)...)
	wechatCh, err = wechat.NewChannel(log, cfg, wechatStatePath, hub, store, ag,
		wrapWeComTools("wechat", wechatTools, wsTools, store, sessionsFn, senderFn))
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

	// 网页入口：账号由 Passkey 验证；会话按稳定 user ID 隔离。
	var webCh *webui.Channel
	var portalSrv *portal.Server
	bodyPortalURL := ""
	if cfg.Web.Domain != "" {
		bodyPortalURL = "https://" + cfg.Web.Domain
	}
	gameBodyUI := bodyui.New(bodySvc, store, hub, ag, cfg.Body.MaxSteps, bodyPortalURL)
	go gameBodyUI.Run(ctx)
	if cfg.Web.Listen != "" {
		acct := account.New(store, log, cfg.Web.Users, cfg.Web.AdminUsers)
		// Old name-only tokens are not evidence of identity. Activation preserves user IDs/data.
		// Legacy name-only sessions cannot prove account ownership and are not imported.

		bodySvc.WithAuthorization(acct.RecheckAdminSession)
		webCh = webui.NewChannel(log, cfg, acct, hub, store, ag,
			wrapWebTools(webBaseTools, wsTools, store, sessionsFn, senderFn)).
			WithMCStatus(mcStatusFn)

		webCh.WithBody(bodySvc, cfg.Body.MaxSteps)
		portalSrv = portal.New(log, cfg, store, acct, webCh).WithBody(bodySvc).WithBodyUI(gameBodyUI.Handler(acct))
		gamesMgr := games.NewManager(log, store)
		// AI 解说（每步点评）：用同一个模型网关，失败不影响对局
		gamesMgr.SetCommentator(commentary.New(cfg.Model.BaseURL, cfg.Model.APIKey, cfg.Model.Name, log).Comment)
		portalSrv.WithGames(games.NewAPI(log, gamesMgr, acct, portalSrv.Auth()).Handler())
		// JCEF 内核镜像（Rinku download-mirror 用；可配重定向到加速源，目录作回源）。
		portalSrv.WithRinkuJCEF(filepath.Join(dataDir, "rinku-jcef"), cfg.Web.RinkuJcefRedirect)
		// 浏览器屏内容反代（/display/* → Paper 出口代理）：屏地址走 HTTPS。
		portalSrv.WithScreenProxy(cfg.Web.ScreenProxy)
		registerPortalApps(portalSrv, store, gw, gamesMgr)
		if cfg.Web.AIPlayerProxy != "" {
			portalSrv.Register(portal.App{ID: "aiplayer", Name: "AI 玩家", Desc: "认识 AI 玩家，阅读它的文章与游戏见闻", Icon: "user", Path: "/ai", Enabled: true, Nav: true, HomeRole: "content", Order: 33, Card: func(context.Context, *storage.User) (any, error) {
				return map[string]any{"type": "link", "title": "AI 玩家", "text": "玩家资料、文章与游戏见闻", "href": "/ai"}, nil
			}})
		}
		if bodySvc.Enabled() {
			portalSrv.Register(portal.App{ID: "body", Name: "AI 身体", Desc: "AI 玩家的状态、审批与控制", Icon: "user", Path: "/body", Enabled: true, AdminOnly: true, Nav: true, HomeRole: "status", Order: 34, Card: func(context.Context, *storage.User) (any, error) {
				profile := bodySvc.Profile()
				return map[string]any{"type": "stat", "title": "AI 身体", "value": profile["status"]}, nil
			}})
		}
		// 域名裸访问（http://zkun.art/）：80 端口已被企微回调占用，非 /wecom 路径转发给门户。
		// 必须在 WithGames 之后取 Handler()——门户 mux 是构建时快照，早取就没有 games 路由。
		httpHandler := portalSrv.Handler()
		if cfg.Web.Domain != "" {
			certDir := cfg.Web.CertDir
			if certDir == "" {
				certDir = filepath.Join(dataDir, "certs")
			}
			portalSrv.EnableTLS(cfg.Web.Domain, certDir, cfg.Web.ACMEEmail)
			// 80 端口兼做 ACME HTTP-01 验证（/.well-known/acme-challenge/...）
			httpHandler = portalSrv.HTTPHandlerForACME(httpHandler)
			tlsAddr := cfg.Web.TLSListen
			if tlsAddr == "" {
				tlsAddr = "[::]:443"
			}
			go func() {
				if err := portalSrv.ServeTLS(tlsAddr); err != nil {
					log.Error("portal https stopped", "err", err)
				}
			}()
			log.Info("portal https enabled", "domain", cfg.Web.Domain, "addr", tlsAddr, "certDir", certDir)
		}
		if wecomCh != nil {
			wecomCh.WithFallback(httpHandler)
		}
		go func() {
			if err := portalSrv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("web ui stopped", "err", err)
			}
		}()
		defer portalSrv.Stop()
		log.Info("web ui enabled", "listen", cfg.Web.Listen, "portal", portalSrv.PortalEnabled(),
			"url", "http://<公网IP>:"+webPort(cfg.Web.Listen))
	} else {
		log.Info("webui channel disabled (web.listen empty)")
	}

	// 提醒调度器：每 20s 扫一次到点提醒，走对应通道的 Deliver 发回原会话。
	deliverers := map[string]reminder.Deliver{
		"minecraft": func(ctx context.Context, _, target, text string) error {
			sess, err := hub.Session(ctx, cfg.Minecraft.SessionID)
			if err != nil {
				return err
			}
			return sess.Reply(ctx, text, target)
		},
	}
	if qqCh != nil {
		deliverers["qq"] = qqCh.Deliver
	}
	if wecomCh != nil {
		deliverers["wecom"] = wecomCh.Deliver
	}
	if aibotCh != nil {
		deliverers["aibot"] = aibotCh.Deliver
	}
	if wechatCh != nil {
		deliverers["wechat"] = wechatCh.Deliver
	}
	if webCh != nil {
		deliverers["web"] = webCh.Deliver
	}
	go reminder.New(store, log, deliverers).Run(ctx)

	handler := func(ctx context.Context, c *ws.Conn, env *protocol.Envelope) {
		if c.RemoteRole() == protocol.RoleBody {
			if authorizeMessage(c.RemoteRole(), env.Type, bodySvc.Attached(c)) {
				bodySvc.Handle(c, env)
			}
			return
		}
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
		case bodyui.RequestType:
			gameBodyUI.Handle(ctx, c, env)
		case protocol.TypeChatMessage:
			var chat protocol.ChatMessage
			if err := env.Decode(&chat); err != nil {
				log.Warn("bad chat.message", "err", err)
				return
			}
			if cfg.Body.Enabled && strings.EqualFold(chat.Player, cfg.Body.Name) {
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
				// 人设 + MC 内置能力组合（管理员改人设/系统提示词后即时生效）。
				if !ag.Submit(agent.Request{
					Session:           sess,
					Player:            chat.Player,
					RequesterID:       chat.UUID,
					Query:             query,
					TriggerMessageID:  msg.ID,
					SystemInstruction: prompts.Resolve("minecraft", "", agent.DefaultCapabilities()),
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
		if c.RemoteRole() == protocol.RoleBody {
			if err := bodySvc.Attach(c, c.Hello()); err != nil {
				c.Reject("body registration rejected")
			}
			return
		}
		if c.RemoteRole() == protocol.RoleMinecraft {
			mc.Attach(c)
			gameBodyUI.Attach(c)
		}
	})
	srv.OnDisconnect(func(c *ws.Conn) {
		if c.RemoteRole() == protocol.RoleBody {
			bodySvc.Detach(c)
			return
		}
		if c.RemoteRole() == protocol.RoleMinecraft {
			gameBodyUI.Detach(c)
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
	bodyui.RequestType:          true,
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
func wrapWeComTools(channel string, base []tool.BaseTool, wsTools *tools.Workspace, store *storage.Store,
	sessions func(ctx context.Context, sessionKey string) (*session.Session, error),
	sender func(ctx context.Context, sess *session.Session, target, text string) error) []tool.BaseTool {
	out := make([]tool.BaseTool, 0, len(base))
	for _, tl := range base {
		out = append(out, &wecomToolGate{inner: tl, channel: channel, ws: wsTools, store: store, sessions: sessions, sender: sender})
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
	if role == protocol.RoleBody {
		switch msgType {
		case protocol.TypeBodyState, protocol.TypeBodyEvent, protocol.TypeBodyResult, protocol.TypeBodyInspectResult, protocol.TypeBodyPolicyAck, protocol.TypeBodyControlAck:
			return attached
		default:
			return false
		}
	}
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
