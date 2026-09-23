package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"mineagent/internal/agent"
	"mineagent/internal/channels/minecraft"
	"mineagent/internal/config"
	"mineagent/internal/protocol"
	"mineagent/internal/qq"
	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
	"mineagent/internal/version"
	"mineagent/internal/ws"
)

func main() {
	configPath := flag.String("config", "config.json", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
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

	// QQ 通道工具集：只读查询 + MC 高权限（转游戏内审批）+ workspace 四件套 + 绑定。
	// 注意 workspace_exec 的管理员门禁不在工具里写死，而是在 Submit 前按消息身份
	// 动态放进 ctx（WithQQAdmin），这样同一套工具对管理员/普通群友表现不同。
	// qq_bind/qq_unbind 的身份同样走 ctx（WithQQIdentity）。
	wsTools := tools.NewWorkspace(cfg.Workspace, cfg.QQ.AdminOpenIDs, store, log)
	bindTools := tools.NewQQBind(store, log)
	qqBaseTools := append(append(tools.ReadOnly(gw), tools.Privileged(gw, approvals, store, log)...),
		append(wsTools.Tools(), bindTools.Tools()...)...)

	ag, err := agent.New(ctx, cfg, store, log, agentTools, approvals)
	if err != nil {
		log.Error("init agent", "err", err)
		os.Exit(1)
	}
	if ag.Enabled() {
		go ag.Run(ctx)
	}

	// QQ 通道：配了 appId+secret 才启动，否则完全不影响 MC 链路。
	var qqCh *qq.Channel
	if cfg.QQ.AppID != "" && cfg.QQ.Secret != "" {
		tokens := qq.NewTokenSource(cfg.QQ.AppID, cfg.QQ.Secret, cfg.QQ.APIBase, log)
		api := qq.NewAPI(cfg.QQ.APIBase, tokens, log)
		qqCh = qq.NewChannel(log, cfg, hub, store, ag, api, wrapQQTools(qqBaseTools, wsTools, store))
		qqCh.Start()
		defer qqCh.Stop()
		log.Info("qq channel enabled")
	} else {
		log.Info("qq channel disabled (qq.appId/appSecret empty)")
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
// session=qq:...、historyCutoff；这里追加：
//   - WithQQAdmin：union/user/member 任一命中 adminOpenIDs 即管理员。
//   - WithQQIdentity：openid 候选列表，供 qq_bind/qq_unbind 用。
//   - WithMCRequester：绑定的 MC 名，供 Gateway.Call 发给插件（requester 字段）。
func wrapQQTools(base []tool.BaseTool, wsTools *tools.Workspace, store *storage.Store) []tool.BaseTool {
	out := make([]tool.BaseTool, 0, len(base))
	for _, tl := range base {
		out = append(out, &qqToolGate{inner: tl, ws: wsTools, store: store})
	}
	return out
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
