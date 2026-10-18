// Test fixture: temporary database and localhost-only HTTP. Never loads runtime config.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mineagent/internal/account"
	"mineagent/internal/config"
	"mineagent/internal/portal"
	"mineagent/internal/storage"
	"mineagent/internal/webui"
)

func main() {
	dir, err := os.MkdirTemp("", "mineagent-passkey-test-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	store, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		panic(err)
	}
	defer store.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	service := account.New(store, log, nil, []string{"owner"})
	code, err := service.IssueEnrollment(ctx, "owner", false)
	if err != nil {
		panic(err)
	}
	user, _ := store.UserByName(ctx, "owner")
	_ = store.EnsureSession(ctx, fmt.Sprintf("web:user:%d", user.ID), time.Now().UnixMilli())
	_, _ = store.AppendMessage(ctx, storage.Message{SessionID: fmt.Sprintf("web:user:%d", user.ID), Channel: "web", AuthorKind: "user", Text: "fixture-history-preserved", CreatedAt: time.Now().UnixMilli()})
	legacyToken := "fixture-legacy-untrusted-session"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(legacyToken)))
	_ = store.CreateAuthSession(ctx, storage.AuthSession{UserID: user.ID, TokenHash: hash, CreatedAt: time.Now().UnixMilli(), ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	origin := fmt.Sprintf("http://localhost:%d", listener.Addr().(*net.TCPAddr).Port)
	cfg := config.Default()
	cfg.Web.DataDir = dir
	cfg.Web.DownloadPublicOrigin = os.Getenv("MINEAGENT_TEST_DOWNLOAD_ORIGIN")
	cfg.Workspace.Root = filepath.Join(dir, "workspace")
	cfg.Web.PasskeyRPID = "localhost"
	cfg.Web.PasskeyOrigins = []string{origin}
	ch := webui.NewChannel(log, cfg, service, nil, store, nil, nil)
	server := portal.New(log, cfg, store, service, ch)
	mux := server.Handler()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture" {
			w.Header().Set("Content-Type", "text/html")
			b, _ := webui.RenderPage("portal/index.html")
			_, _ = w.Write([]byte(strings.Replace(string(b), "/js/portal.js", "/js/passkey-fixture.js", 1)))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/js/passkey-fixture.js") {
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(w, `import {passkeyForm} from './passkeys.js'; document.body.replaceChildren(passkeyForm((data)=>{document.body.textContent='Authenticated '+data.name; window.testToken=data.token;}));`)
			return
		}
		mux.ServeHTTP(w, r)
	})
	// This output is consumed only by the browser test process; contains exclusively ephemeral test secrets.
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"origin": origin, "code": code, "legacyToken": legacyToken})
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); listener.Close() }()
	_ = http.Serve(listener, handler)
}
