package account

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mineagent/internal/storage"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPasswordSetupLoginAndReset(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, []string{"owner"})
	existing, _ := store.EnsureUser(ctx, "owner", 1)
	_ = store.CreateAuthSession(ctx, storage.AuthSession{UserID: existing.ID, TokenHash: hashToken("legacy"), ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})
	handler := NewPasswords(service, []string{"https://example.com"})
	post := func(action, body, origin string) (int, map[string]any) {
		r := httptest.NewRequest("POST", "/api/auth/password/"+action, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var data map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &data)
		return w.Code, data
	}
	if code, _ := post("status", `{"name":"owner"}`, "https://evil.example"); code != 403 {
		t.Fatal("foreign origin accepted")
	}
	code, data := post("status", `{"name":"owner"}`, "https://example.com")
	if code != 200 || data["needsSetup"] != true || data["newAccount"] != false {
		t.Fatal("old account not offered setup")
	}
	if code, fresh := post("status", `{"name":"fresh"}`, "https://example.com"); code != 200 || fresh["newAccount"] != true {
		t.Fatal("new account status incorrect")
	}
	if code, _ = post("setup", `{"name":"owner","password":"long test phrase!","confirm":"different"}`, "https://example.com"); code != 400 {
		t.Fatal("mismatched password accepted")
	}
	code, data = post("setup", `{"name":"owner","password":"long test phrase!","confirm":"long test phrase!"}`, "https://example.com")
	if code != 200 {
		t.Fatalf("setup failed: %v", data)
	}
	token := data["token"].(string)
	if data["admin"] != true || int64(data["id"].(float64)) != existing.ID {
		t.Fatal("lost account identity")
	}
	if service.Current(ctx, "legacy") != nil {
		t.Fatal("legacy session survived")
	}
	if service.Current(ctx, token) == nil {
		t.Fatal("password session unverified")
	}
	stored, _ := store.PasswordHash(ctx, existing.ID)
	if !strings.HasPrefix(stored, argonPrefix) || strings.Contains(stored, "long test phrase!") {
		t.Fatal("password not hashed")
	}
	if code, _ = post("setup", `{"name":"owner","password":"replacement phrase","confirm":"replacement phrase"}`, "https://example.com"); code != 409 {
		t.Fatal("password overwritten")
	}
	if code, _ = post("login", `{"name":"owner","password":"wrong test phrase"}`, "https://example.com"); code != 401 {
		t.Fatal("wrong password accepted")
	}
	if code, _ = post("login", `{"name":"owner","password":"long test phrase!"}`, "https://example.com"); code != 200 {
		t.Fatal("correct password rejected")
	}
	if err = service.ResetPassword(ctx, "owner"); err != nil {
		t.Fatal(err)
	}
	if service.Current(ctx, token) != nil {
		t.Fatal("reset left session active")
	}
	if hash, _ := store.PasswordHash(ctx, existing.ID); hash != "" {
		t.Fatal("reset left password active")
	}
}
func TestInitialPasswordCannotRaceOrReplacePasskey(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	u, _ := store.EnsureUser(ctx, "owner", 1)
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for _, hash := range []string{"hash-one", "hash-two"} {
		wg.Add(1)
		go func(hash string) { defer wg.Done(); out <- store.SetInitialPassword(ctx, u.ID, hash) }(hash)
	}
	wg.Wait()
	close(out)
	success := 0
	for err := range out {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("concurrent password overwrite")
	}
	user, _ := store.EnsureUser(ctx, "passkey", 1)
	_ = store.CreatePasskeyGrant(ctx, user.ID, []byte("handle"), "grant", time.Now().Add(time.Hour).UnixMilli(), false)
	if err = store.CommitPasskeyEnrollment(ctx, "grant", user.ID, []byte("key"), []byte(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	if store.SetInitialPassword(ctx, user.ID, "hash") == nil {
		t.Fatal("passkey protected account claimed by name")
	}
}

var _ http.Handler = (*Passwords)(nil)
