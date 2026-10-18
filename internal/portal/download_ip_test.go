package portal

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signDownloadIP(r *http.Request, ip, token, signingSecret string, stamp time.Time) {
	seconds := strconv.FormatInt(stamp.Unix(), 10)
	key, _ := hex.DecodeString(signingSecret)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.Join([]string{"v1", seconds, r.Method, r.URL.Path, ip, downloadHash(token)}, "\n")))
	r.Header.Set("X-Download-Client-IP", ip)
	r.Header.Set("X-Download-Timestamp", seconds)
	r.Header.Set("X-Download-Signature", hex.EncodeToString(mac.Sum(nil)))
}

func TestDownloadVisitorProof(t *testing.T) {
	s, _ := downloadFixture(t)
	s.cfg.DownloadProxySecret = strings.Repeat("ab", 32)
	now := time.Now()
	for _, tc := range []struct {
		name, ip string
		change   func(*http.Request)
		invalid  bool
	}{
		{"IPv4", "198.51.100.8", nil, false},
		{"IPv6", "2001:db8::8", nil, false},
		{"tampered address", "198.51.100.8", func(r *http.Request) { r.Header.Set("X-Download-Client-IP", "198.51.100.9") }, true},
		{"tampered token", "198.51.100.8", func(r *http.Request) {
			signDownloadIP(r, "198.51.100.8", "other-token", s.cfg.DownloadProxySecret, now)
		}, true},
		{"tampered method", "198.51.100.8", func(r *http.Request) { r.Method = "GET" }, true},
		{"tampered path", "198.51.100.8", func(r *http.Request) { r.URL.Path = "/api/downloads/info" }, true},
		{"stale", "198.51.100.8", func(r *http.Request) {
			signDownloadIP(r, "198.51.100.8", "token", s.cfg.DownloadProxySecret, now.Add(-121*time.Second))
		}, true},
		{"future", "198.51.100.8", func(r *http.Request) {
			signDownloadIP(r, "198.51.100.8", "token", s.cfg.DownloadProxySecret, now.Add(121*time.Second))
		}, true},
		{"wrong signing key", "198.51.100.8", func(r *http.Request) { signDownloadIP(r, "198.51.100.8", "token", strings.Repeat("cd", 32), now) }, true},
		{"requires TLS", "198.51.100.8", func(r *http.Request) { r.TLS = nil }, true},
		{"invalid IP", "invalid", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://download.example/api/downloads/file", nil)
			r.TLS = &tls.ConnectionState{}
			signDownloadIP(r, tc.ip, "token", s.cfg.DownloadProxySecret, now)
			if tc.change != nil {
				tc.change(r)
			}
			got, err := s.downloadVisitorIP(r, "token", now)
			if (err != nil) != tc.invalid || (!tc.invalid && got != tc.ip) {
				t.Fatal("visitor proof accepted or rejected incorrectly")
			}
		})
	}
	r := httptest.NewRequest("POST", "/api/downloads/file", nil)
	r.RemoteAddr = "192.0.2.10:4567"
	for _, h := range []string{"CF-Connecting-IP", "X-Forwarded-For", "X-Real-IP"} {
		r.Header.Set(h, "198.51.100.99")
	}
	got, err := s.downloadVisitorIP(r, "token", now)
	if err != nil || got != "192.0.2.10" {
		t.Fatal("unsigned public headers trusted")
	}
}

func TestSignedDownloadRecordsVisitorAndRejectsForgeryBeforeCounting(t *testing.T) {
	s, call := downloadFixture(t)
	f, token := uploadDownloadFixture(t, call)
	s.cfg.DownloadProxySecret = strings.Repeat("ab", 32)
	for _, valid := range []bool{true, false} {
		r := httptest.NewRequest("POST", "https://download.example/api/downloads/file", strings.NewReader("key="+token))
		r.RemoteAddr = "192.0.2.10:4567"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		signDownloadIP(r, "198.51.100.8", token, s.cfg.DownloadProxySecret, time.Now())
		if !valid {
			r.Header.Set("X-Download-Client-IP", "198.51.100.99")
		}
		w := httptest.NewRecorder()
		s.handleDownloads(w, r)
		want := 200
		if !valid {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("status %d, want %d", w.Code, want)
		}
	}
	events, err := s.store.DownloadEvents(context.Background(), f.ID)
	if err != nil || len(events) != 1 || events[0].IP != "198.51.100.8" || events[0].Status != "completed" {
		t.Fatal("visitor IP not recorded correctly or forgery counted")
	}
}
