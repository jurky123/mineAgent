package games

import (
	"mineagent/internal/storage"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicCatalogDoesNotOpenRoomOrMutationAccess(t *testing.T) {
	calls := 0
	auth := func(next func(http.ResponseWriter, *http.Request, *storage.User)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(401) }
	}
	h := NewAPI(nil, testManager(t), nil, auth).Handler()
	for _, tc := range []struct {
		method, path string
		code         int
	}{{"GET", "/api/games", 200}, {"POST", "/api/games", 405}, {"GET", "/api/games/chess/rooms", 401}, {"POST", "/api/games/chess/rooms", 401}, {"POST", "/api/games/chess/move", 401}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.code {
			t.Fatalf("%s %s = %d", tc.method, tc.path, w.Code)
		}
	}
	if calls != 3 {
		t.Fatalf("room auth bypassed: calls %d", calls)
	}
}
