package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"mineagent/aiplayer"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	configFile := flag.String("config", "", "independent AI portal JSON config (required)")
	action := flag.String("action", "serve", "serve|admin|issue-token|revoke-token|backup")
	admin := flag.String("admin", "admin", "local admin name")
	passwordFile := flag.String("password-file", "", "owner-only password file")
	agent := flag.String("agent", "main", "configured agent ID")
	tokenFile := flag.String("token-file", "", "NEW owner-only file to receive AI writing token")
	tokenID := flag.String("token-id", "", "token ID to revoke")
	ttl := flag.Duration("ttl", 30*24*time.Hour, "writing token lifetime, at most 366 days")
	output := flag.String("output", "", "new persistent absolute backup file")
	flag.Parse()
	if *configFile == "" {
		return errors.New("--config is required; never point this at MineAgent config.json")
	}
	c, err := aiplayer.LoadConfig(*configFile)
	if err != nil {
		return err
	}
	store, err := aiplayer.Open(c.Database)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err = store.Seed(ctx, c.Agents); err != nil {
		return err
	}
	switch *action {
	case "admin":
		info, err := os.Lstat(*passwordFile)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024 {
			return errors.New("password file must be owner-only, regular and at most 1024 bytes")
		}
		data, err := os.ReadFile(*passwordFile)
		if err != nil {
			return errors.New("cannot read password file")
		}
		if err = store.CreateAdmin(ctx, *admin, strings.TrimSpace(string(data))); err != nil {
			return err
		}
		fmt.Println("Admin password set; prior sessions revoked.")
		return nil
	case "issue-token":
		f, err := os.OpenFile(*tokenFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("token-file must be a NEW private file")
		}
		id, raw, err := store.IssueToken(ctx, *agent, time.Now().Add(*ttl))
		if err != nil {
			f.Close()
			os.Remove(*tokenFile)
			return err
		}
		if _, err = f.WriteString(raw + "\n"); err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = store.RevokeToken(ctx, id)
			os.Remove(*tokenFile)
			return errors.New("failed to persist token; credential revoked")
		}
		fmt.Println("Issued token ID:", id, "(secret written only to token-file)")
		return nil
	case "revoke-token":
		return store.RevokeToken(ctx, *tokenID)
	case "backup":
		clean := filepath.Clean(*output)
		if !filepath.IsAbs(clean) || clean == "/tmp" || strings.HasPrefix(clean, "/tmp/") {
			return errors.New("backup requires a new absolute destination outside /tmp")
		}
		if err = store.Backup(ctx, clean); err != nil {
			return err
		}
		fmt.Println("Consistent backup created.")
		return nil
	case "serve":
		log := slog.New(slog.NewTextHandler(os.Stderr, nil))
		profiles := aiplayer.NewProfiles(c, log)
		app, err := aiplayer.NewServer(c, store, profiles, log)
		if err != nil {
			return err
		}
		go profiles.Run(ctx)
		srv := &http.Server{Addr: c.Listen, Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
		shutdownDone := make(chan struct{})
		go func() {
			defer close(shutdownDone)
			<-ctx.Done()
			shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			if err := srv.Shutdown(shutdown); err != nil {
				_ = srv.Close()
			}
		}()
		log.Info("AI player portal listening on configured loopback interface", "profileConfigured", c.Profile != nil, "writingAdapter", "not_connected")
		err = srv.ListenAndServe()
		cancel()
		<-shutdownDone
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.New("AI portal HTTP server failed")
		}
		return nil
	default:
		return errors.New("unknown action")
	}
}
