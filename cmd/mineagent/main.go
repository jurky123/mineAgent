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

	"mineagent/internal/agent"
	"mineagent/internal/channels/minecraft"
	"mineagent/internal/config"
	"mineagent/internal/protocol"
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

	hub := session.NewHub(store, log)
	sess, err := hub.Session(ctx, cfg.Minecraft.SessionID)
	if err != nil {
		log.Error("open session", "err", err)
		os.Exit(1)
	}
	mc := minecraft.NewChannel(log)
	sess.Register(mc)

	gw := tools.NewGateway(mc, log)
	ag, err := agent.New(ctx, cfg, store, log, tools.ReadOnly(gw))
	if err != nil {
		log.Error("init agent", "err", err)
		os.Exit(1)
	}
	if ag.Enabled() {
		go ag.Run(ctx)
	}

	handler := func(ctx context.Context, c *ws.Conn, env *protocol.Envelope) {
		switch env.Type {
		case protocol.TypeChatMessage:
			var chat protocol.ChatMessage
			if err := env.Decode(&chat); err != nil {
				log.Warn("bad chat.message", "err", err)
				return
			}
			if _, err := sess.Ingest(ctx, storage.Message{
				Channel:    "minecraft",
				AuthorKind: "player",
				AuthorID:   chat.UUID,
				AuthorName: chat.Player,
				Text:       chat.Message,
			}); err != nil {
				log.Error("ingest chat", "err", err)
				return
			}
			if query, ok := matchTrigger(chat.Message, cfg.Minecraft.Trigger); ok {
				log.Info("agent trigger", "player", chat.Player, "query", query)
				if !ag.Submit(agent.Request{Session: sess, Player: chat.Player, Query: query}) {
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
