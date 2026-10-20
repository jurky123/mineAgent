package portal

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAIPlayerProxyRoutesAndCredentialIsolation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || strings.Contains(r.Header.Get("Cookie"), "mineagent") {
			t.Error("MineAgent credentials crossed publishing boundary")
		}
		if r.Header.Get("Origin") != "https://example.com" || r.Header.Get("X-CSRF-Token") != "publishing-csrf" {
			t.Error("publishing CSRF context was lost")
		}
		if c, err := r.Cookie("__Host-aiplayer_admin"); err != nil || c.Value != "publishing-session" {
			t.Error("publishing cookie was lost")
		}
		io.WriteString(w, r.Method+" "+r.URL.RequestURI())
	}))
	defer upstream.Close()
	mux := http.NewServeMux()
	mountAIPlayer(mux, upstream.URL)
	for _, path := range []string{"/ai", "/ai/main", "/ai-assets/app.js", "/api/ai-agents", "/api/ai-posts/p1?version=2", "/api/ai-auth/me", "/api/ai-moderation/queue"} {
		r := httptest.NewRequest("GET", "https://example.com"+path, nil)
		r.Header.Set("Authorization", "Bearer mineagent-private")
		r.Header.Set("Cookie", "mineagent=private; __Host-aiplayer_admin=publishing-session")
		r.Header.Set("Origin", "https://example.com")
		r.Header.Set("X-CSRF-Token", "publishing-csrf")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != "GET "+path {
			t.Fatalf("route %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/body/control", "/api/auth/me", "/healthz", "/ai-other"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("unrelated route %s proxied", path)
		}
	}
}

func TestAIPlayerProxyRejectsNonLoopbackAndUnavailableUpstream(t *testing.T) {
	for _, target := range []string{"http://example.com", "http://127.0.0.1:1", "http://127.0.0.1:8890/path", "http://user:password@127.0.0.1:8890"} {
		mux := http.NewServeMux()
		mountAIPlayer(mux, target)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/ai", nil))
		if w.Code != 503 {
			t.Fatalf("%s: %d", target, w.Code)
		}
	}
}
