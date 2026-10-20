package portal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mineagent/internal/account"
	"mineagent/internal/body"
	"mineagent/internal/config"
	"mineagent/internal/session"
	"mineagent/internal/storage"
	"mineagent/internal/webui"
)

func bodyPlayerFixture(t *testing.T) (*Server, *storage.Store, string) {
	t.Helper()
	st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	cfg.Web.DataDir = t.TempDir()
	cfg.Body.Enabled = true
	cfg.Body.Mode = "assist"
	cfg.Body.ID = "main"
	cfg.Web.AIPlayerProxy = "http://127.0.0.1:1"
	user, err := st.EnsureUser(ctx, "admin", time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetInitialPassword(ctx, user.ID, "fixture-password-hash"); err != nil {
		t.Fatal(err)
	}
	token := "fixture-main-admin-session"
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	if err = st.CreateAuthSession(ctx, storage.AuthSession{UserID: user.ID, TokenHash: hash, CreatedAt: time.Now().UnixMilli(), ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err = st.MarkPasswordSession(ctx, hash, "fixture-password-hash"); err != nil {
		t.Fatal(err)
	}
	acct := account.New(st, log, nil, []string{"admin"})
	web := webui.NewChannel(log, cfg, acct, session.NewHub(st, log), st, nil, nil)
	core := body.New(cfg.Body, st, nil, log)
	core.WithAuthorization(acct.RecheckAdminSession)
	return New(log, cfg, st, acct, web).WithBody(core), st, token
}
func TestBodyPlayerBindingAndExistingAuthorization(t *testing.T) {
	s, _, token := bodyPlayerFixture(t)
	h := s.Handler()
	request := func(method, path, auth, cookie, origin, payload string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://example.com"+path, strings.NewReader(payload))
		if auth != "" {
			r.Header.Set("Authorization", "Bearer "+auth)
		}
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	info := request("GET", "/api/body/ui?agent=main", "", "", "", "")
	if info.Code != 200 || !strings.Contains(info.Body.String(), `"available":true`) || !strings.Contains(info.Body.String(), `"profileURL":"/ai/main"`) {
		t.Fatal("missing player routing metadata", info.Body.String())
	}
	for _, field := range []string{"state", "position", "controlEpoch", "token", "health"} {
		var fields map[string]any
		_ = json.Unmarshal(info.Body.Bytes(), &fields)
		if _, ok := fields[field]; ok {
			t.Fatal("private metadata leaked", field)
		}
	}
	info = request("GET", "/api/body/ui?agent=other", "", "", "", "")
	if !strings.Contains(info.Body.String(), `"available":false`) {
		t.Fatal("other player inherited main body")
	}
	for _, path := range []string{"/api/body/status?agent=other", "/api/body/approvals?agent=other", "/api/body/control?agent=other", "/api/body/events?agent=other", "/api/body/task/send?agent=other", "/api/body/minecraft/link?agent=other", "/api/body/status?agent=main&agent=other"} {
		if strings.Contains(path, "minecraft/link") {
			s.WithBodyUI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("wrong-player request reached game bridge") }))
			h = s.Handler()
		}
		w := request("POST", path, token, "", "http://example.com", `{"action":"stop"}`)
		if w.Code != 404 {
			t.Fatalf("wrong player %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/body/status?agent=main", "/api/body/task/history?agent=main", "/api/body/task/send?agent=main"} {
		for _, cookie := range []string{"", "__Host-aiplayer_admin=publishing-admin", "aiplayer_local_admin=publishing-admin"} {
			w := request("GET", path, "", cookie, "", "")
			if w.Code != 403 {
				t.Fatalf("publishing/anonymous identity accepted %s: %d", path, w.Code)
			}
		}
	}
	if w := request("GET", "/api/body/status?agent=main", token, "", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"bodyId":"main"`) {
		t.Fatal("verified admin status failed", w.Code)
	}
	if w := request("GET", "/api/body/status", token, "", "", ""); w.Code != 200 {
		t.Fatal("legacy body API broken")
	}
	if w := request("POST", "/api/body/task/send?agent=main", token, "", "http://evil.example", `{"text":"test"}`); w.Code != 403 {
		t.Fatal("cross-origin task accepted")
	}
	if w := request("GET", "/body", "", "", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "body-game-link") {
		t.Fatal("legacy pairing shell broken")
	}
}
func TestPlayerTaskEntryReusesWebAPIAndRechecksSession(t *testing.T) {
	s, _, token := bodyPlayerFixture(t)
	calls := 0
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/send" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("task delegation changed identity/protocol")
		}
		w.WriteHeader(200)
	})
	h := s.bodyTask(api)
	makeRequest := func(path string) *http.Request {
		r := httptest.NewRequest("POST", "http://example.com"+path, strings.NewReader(`{"text":"test"}`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, makeRequest("/api/body/task/send?agent=main"))
	if w.Code != 200 || calls != 1 {
		t.Fatal("existing task API not reused", w.Code)
	}
	s.body.WithAuthorization(func(context.Context, int64, int64) bool { return false })
	w = httptest.NewRecorder()
	h.ServeHTTP(w, makeRequest("/api/body/task/send?agent=main"))
	if w.Code != 403 || calls != 1 {
		t.Fatal("revoked admin reached task API")
	}
}
