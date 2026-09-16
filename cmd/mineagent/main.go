package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mineagent/internal/config"
	"mineagent/internal/protocol"
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

	handler := func(ctx context.Context, c *ws.Conn, env *protocol.Envelope) {
		log.Info("message", "role", c.RemoteRole(), "type", env.Type, "bytes", len(env.Data))
	}
	srv := ws.New(cfg, log, handler)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
