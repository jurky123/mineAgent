package portal

import (
	"context"
	"mineagent/internal/storage"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicHomeUsesOnlyExplicitSafeCards(t *testing.T) {
	s := &Server{}
	privateCalls, publicCalls := 0, 0
	sensitive := func(context.Context, *storage.User) (any, error) {
		privateCalls++
		return map[string]any{"secret": "private-history"}, nil
	}
	safe := func(_ context.Context, u *storage.User) (any, error) {
		if u != nil {
			t.Fatal("public card got an identity")
		}
		publicCalls++
		return map[string]any{"online": true, "count": 0}, nil
	}
	s.Register(App{ID: "agent", Enabled: true, Card: sensitive}, App{ID: "minecraft", Enabled: true, PublicCard: safe}, App{ID: "admin", Enabled: true, AdminOnly: true, PublicCard: sensitive}, App{ID: "disabled", PublicCard: sensitive})
	w := httptest.NewRecorder()
	s.handlePublicHome(w, httptest.NewRequest("GET", "/api/portal/public", nil))
	if w.Code != 200 || privateCalls != 0 || publicCalls != 1 || strings.Contains(w.Body.String(), "private-history") || strings.Contains(w.Body.String(), "agent") {
		t.Fatalf("unsafe summary: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.handlePublicHome(w, httptest.NewRequest("POST", "/api/portal/public", nil))
	if w.Code != 405 || publicCalls != 1 {
		t.Fatal("public summary accepted a mutation")
	}
	w = httptest.NewRecorder()
	s.handlePage(w, httptest.NewRequest("GET", "/missing", nil))
	if w.Code != 404 || !strings.Contains(w.Body.String(), "返回首页") {
		t.Fatalf("missing branded 404: %d", w.Code)
	}
}
