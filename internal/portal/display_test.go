package portal

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mineagent/internal/account"
	"mineagent/internal/config"
	"mineagent/internal/storage"
)

func TestDisplayAuthorizationIsEnforcedBeforeProxy(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acct := account.New(store, log, nil, []string{"admin"})
	ctx := context.Background()
	tokens := map[string]string{}
	for _, name := range []string{"reader", "admin"} {
		u, err := store.EnsureUser(ctx, name, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.SetInitialPassword(ctx, u.ID, "test-hash"); err != nil {
			t.Fatal(err)
		}
		token := "fixture-" + name
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
		if err = store.CreateAuthSession(ctx, storage.AuthSession{UserID: u.ID, TokenHash: hash, CreatedAt: 1, ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkPasswordSession(ctx, hash, "test-hash"); err != nil {
			t.Fatal(err)
		}
		tokens[name] = token
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	tokenPath := filepath.Join(dir, "control-token")
	if err = os.WriteFile(tokenPath, []byte("ephemeral-fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Web.DisplayAPI = upstream.URL
	cfg.Web.DisplayTokenFile = tokenPath
	server := New(log, cfg, store, acct, nil)
	for _, tc := range []struct {
		method, path, user string
		status             int
	}{{"POST", "/api/display/close", "reader", 403}, {"POST", "/api/display/whitelist", "reader", 403}, {"GET", "/api/display/whitelist", "reader", 403}, {"GET", "/api/display/status", "reader", 200}, {"POST", "/api/display/whitelist", "admin", 200}} {
		before := calls
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"action":"add","host":"example.com"}`))
		r.Header.Set("Authorization", "Bearer "+tokens[tc.user])
		w := httptest.NewRecorder()
		server.handleDisplayProxy(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s %s: %d", tc.user, tc.method, tc.path, w.Code)
		}
		if tc.status == 403 && calls != before {
			t.Fatal("unauthorized request reached upstream")
		}
	}
}
