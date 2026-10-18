package portal

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mineagent/internal/account"
	"mineagent/internal/config"
	"mineagent/internal/storage"
)

func downloadFixture(t *testing.T) (*Server, func(string, string, string, string) *httptest.ResponseRecorder) {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acct := account.New(store, log, nil, []string{"admin"})
	ctx := context.Background()
	for _, name := range []string{"reader", "admin"} {
		u, err := store.EnsureUser(ctx, name, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetInitialPassword(ctx, u.ID, "test-hash"); err != nil {
			t.Fatal(err)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte("fixture-"+name)))
		if err := store.CreateAuthSession(ctx, storage.AuthSession{UserID: u.ID, TokenHash: hash, CreatedAt: 1, ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkPasswordSession(ctx, hash, "test-hash"); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Web.DataDir = dir
	s := New(log, cfg, store, acct, nil)
	call := func(method, path, body, user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "[2001:db8::2]:4567"
		r.Header.Set("X-Forwarded-For", "198.51.100.99")
		if user != "" {
			r.Header.Set("Authorization", "Bearer fixture-"+user)
		}
		if strings.Contains(path, "/info") || strings.Contains(path, "/file") {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		w := httptest.NewRecorder()
		s.handleDownloads(w, r)
		return w
	}
	return s, call
}

func uploadDownloadFixture(t *testing.T, call func(string, string, string, string) *httptest.ResponseRecorder) (storage.DownloadFile, string) {
	t.Helper()
	w := call("POST", "/api/downloads?name=tool.bin", "fixture contents", "admin")
	if w.Code != 201 {
		t.Fatalf("upload status %d", w.Code)
	}
	var result struct {
		File storage.DownloadFile `json:"file"`
		Link string               `json:"link"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	key := strings.TrimPrefix(result.Link, "/download#")
	if !validDownloadSecret(key) {
		t.Fatal("no valid download link")
	}
	return result.File, key
}

func TestDownloadAuthorizationAndTransport(t *testing.T) {
	s, call := downloadFixture(t)
	for _, path := range []string{"/api/downloads", "/api/downloads/events?id=anything", "/api/downloads/link", "/api/downloads/revoke"} {
		for _, user := range []string{"", "reader"} {
			want := 401
			if user == "reader" {
				want = 403
			}
			if w := call("POST", path, `{}`, user); w.Code != want {
				t.Fatalf("authorization %s: %d", path, w.Code)
			}
		}
	}
	if _, err := os.Stat(s.downloadDir()); !os.IsNotExist(err) {
		t.Fatal("unauthorized request created upload directory")
	}
	for _, header := range []string{"Origin", "Sec-Fetch-Site"} {
		r := httptest.NewRequest("POST", "/api/downloads?name=evil.bin", strings.NewReader("file"))
		r.Header.Set("Authorization", "Bearer fixture-admin")
		if header == "Origin" {
			r.Header.Set(header, "https://other.example")
		} else {
			r.Header.Set(header, "cross-site")
		}
		w := httptest.NewRecorder()
		s.handleDownloads(w, r)
		if w.Code != 403 {
			t.Fatalf("cross site %s accepted", header)
		}
	}
	s.tlsDomain = "example.com"
	if w := call("POST", "/api/downloads?name=tool.bin", "file", "admin"); w.Code != 426 {
		t.Fatal("HTTP upload accepted")
	}
	if w := call("POST", "/api/downloads/file", "key=x", ""); w.Code != 426 {
		t.Fatal("HTTP download accepted")
	}
	r := httptest.NewRequest("GET", "https://example.com/api/downloads", nil)
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Authorization", "Bearer fixture-admin")
	w := httptest.NewRecorder()
	s.handleDownloads(w, r)
	if w.Code != 200 {
		t.Fatalf("HTTPS admin rejected %d", w.Code)
	}
}

func TestDownloadCountsPeerAddressRevocationAndPersistence(t *testing.T) {
	s, call := downloadFixture(t)
	f, key := uploadDownloadFixture(t, call)
	info, err := os.Stat(filepath.Join(s.downloadDir(), f.ID))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("file permissions")
	}
	dirInfo, _ := os.Stat(s.downloadDir())
	if dirInfo.Mode().Perm() != 0700 {
		t.Fatal("directory permissions")
	}
	for _, method := range []string{"GET", "HEAD"} {
		if w := call(method, "/api/downloads/file?key="+key, "", ""); w.Code != 405 {
			t.Fatal("unsafe method accepted")
		}
	}
	if w := call("POST", "/api/downloads/file?key="+key, "", ""); w.Code != 404 {
		t.Fatal("query secret accepted")
	}
	if w := call("POST", "/api/downloads/info", "key="+key, ""); w.Code != 200 || strings.Contains(w.Body.String(), f.ID) {
		t.Fatal("metadata unavailable or leaks id")
	}
	files, _ := s.store.ListDownloadFiles(context.Background())
	if files[0].Requests != 0 {
		t.Fatal("page/metadata counted as download")
	}
	// Concurrent explicit downloads count independently, using the TCP peer rather than a forged header.
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := call("POST", "/api/downloads/file", "key="+key, "")
			if w.Code != 200 || w.Body.String() != "fixture contents" {
				t.Error("download failed")
			}
			if w.Header().Get("Cache-Control") != "no-store, private" || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
				t.Error("unsafe response headers")
			}
		}()
	}
	wg.Wait()
	files, _ = s.store.ListDownloadFiles(context.Background())
	if files[0].Requests != 6 || files[0].Completed != 6 {
		t.Fatal("incorrect counts")
	}
	events, _ := s.store.DownloadEvents(context.Background(), f.ID)
	if len(events) != 6 || events[0].IP != "2001:db8::2" || events[0].BytesSent != 16 {
		t.Fatal("incorrect audit records")
	}
	w := call("POST", "/api/downloads/link", fmt.Sprintf(`{"id":%q}`, f.ID), "admin")
	if w.Code != 200 {
		t.Fatal("rotate failed")
	}
	var result struct {
		Link string `json:"link"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if w := call("POST", "/api/downloads/file", "key="+key, ""); w.Code != 404 {
		t.Fatal("old link survived rotation")
	}
	newKey := strings.TrimPrefix(result.Link, "/download#")
	if w := call("POST", "/api/downloads/file", "key="+newKey, ""); w.Code != 200 {
		t.Fatal("new link unusable")
	}
	if w := call("POST", "/api/downloads/revoke", fmt.Sprintf(`{"id":%q}`, f.ID), "admin"); w.Code != 200 {
		t.Fatal("revoke failed")
	}
	if w := call("POST", "/api/downloads/info", "key="+newKey, ""); w.Code != 404 {
		t.Fatal("revoked link available")
	}
	if w := call("POST", "/api/downloads/file", "key="+newKey, ""); w.Code != 404 {
		t.Fatal("revoked file available")
	}
	// A fresh connection to the same database retains all events and revocation.
	other, err := storage.Open(filepath.Join(s.cfg.DataDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	files, err = other.ListDownloadFiles(context.Background())
	if err != nil || files[0].Requests != 7 || files[0].Enabled {
		t.Fatal("state not persisted")
	}
}

type failedDownloadWriter struct{ header http.Header }

func (w failedDownloadWriter) Header() http.Header       { return w.header }
func (w failedDownloadWriter) WriteHeader(int)           {}
func (w failedDownloadWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestDownloadMissingAndInterruptedTransfers(t *testing.T) {
	s, call := downloadFixture(t)
	f, key := uploadDownloadFixture(t, call)
	// no-referrer native forms send Origin: null in browsers; possession of the link is required.
	form := httptest.NewRequest("POST", "/api/downloads/file", strings.NewReader("key="+key))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	form.Header.Set("Origin", "null")
	form.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	s.handleDownloads(response, form)
	if response.Code != 200 {
		t.Fatal("native no-referrer download rejected")
	}
	r := httptest.NewRequest("POST", "/api/downloads/file", strings.NewReader("key="+key))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.handleDownloads(failedDownloadWriter{make(http.Header)}, r)
	events, _ := s.store.DownloadEvents(context.Background(), f.ID)
	if len(events) != 2 || events[0].Status != "interrupted" || events[0].BytesSent != 0 {
		t.Fatal("interruption misreported")
	}
	if err := os.Remove(filepath.Join(s.downloadDir(), f.ID)); err != nil {
		t.Fatal(err)
	}
	if w := call("POST", "/api/downloads/file", "key="+key, ""); w.Code != 404 {
		t.Fatal("missing file not rejected")
	}
	files, _ := s.store.ListDownloadFiles(context.Background())
	if files[0].Requests != 2 || files[0].Completed != 1 {
		t.Fatal("missing file counted")
	}
	r = httptest.NewRequest("POST", "/api/downloads?name=large.bin", nil)
	r.ContentLength = maxDownloadUpload + 1
	r.Header.Set("Authorization", "Bearer fixture-admin")
	w := httptest.NewRecorder()
	s.handleDownloads(w, r)
	if w.Code != 413 {
		t.Fatal("oversized upload accepted")
	}
}
