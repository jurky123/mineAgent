package portal

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mineagent/internal/account"
	"mineagent/internal/body"
	"mineagent/internal/config"
	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/webui"
)

func TestBodyPairingShellAllowsLoginWithoutExposingState(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{}
	cfg.Body = config.DefaultBody()
	cfg.Body.Enabled = true
	cfg.Body.Mode = "assist"
	cfg.Web.DataDir = t.TempDir()
	acct := account.New(store, log, nil, []string{"admin"})
	web := webui.NewChannel(log, cfg, acct, session.NewHub(store, log), store, nil, nil)
	core := body.New(cfg.Body, store, nil, log)
	h := New(log, cfg, store, acct, web).WithBody(core).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://example.com/body", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "body-game-link") {
		t.Fatal("first-time user cannot reach login-enabled pairing shell", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://example.com/api/body/status", nil))
	if w.Code != 403 {
		t.Fatal("anonymous shell access exposed body state", w.Code)
	}
}
