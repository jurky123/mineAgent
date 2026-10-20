package portal

import (
	"net/http"
	"net/url"
	"strings"

	"mineagent/internal/body"
)

// Only public routing metadata lives here. Credentials and private body data
// stay in MineAgent; the independent profile service never proxies them.
func (s *Server) bodyPlayerInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	id := s.bodyID()
	selected := r.URL.Query()["agent"]
	if id == "" || len(selected) > 1 || (len(selected) == 1 && selected[0] != id) {
		writeJSON(w, 200, map[string]any{"available": false})
		return
	}
	profileURL := ""
	if s.cfg.AIPlayerProxy != "" {
		profileURL = "/ai/" + url.PathEscape(id)
	}
	writeJSON(w, 200, map[string]any{"available": true, "id": id, "module": "/static/js/body-panel.js", "stylesheet": "/static/css/body.css", "profileURL": profileURL})
}
func (s *Server) bodyID() string {
	if s.body == nil || !s.body.Enabled() {
		return ""
	}
	id, _ := s.body.Profile()["id"].(string)
	return id
}

// Original paths without agent remain compatible with deployed game clients.
// An explicit selection is checked on the server for every read/write/SSE.
func (s *Server) bodyPlayer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ids, ok := r.URL.Query()["agent"]; ok && (len(ids) != 1 || ids[0] == "" || ids[0] != s.bodyID()) {
			writeJSON(w, 404, map[string]string{"error": "body_not_found"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Reuse the existing authenticated Agent conversation and runner. This is a
// player-bound entry, not another executor, identity exchange or model loop.
func (s *Server) bodyTask(api http.Handler) http.Handler {
	return s.bodyPlayer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids := r.URL.Query()["agent"]
		if len(ids) != 1 || ids[0] == "" {
			writeJSON(w, 400, map[string]string{"error": "body_required"})
			return
		}
		p := s.body.Admin(r, s.acct)
		if p == nil || s.body.Check(r.Context(), p, true) != nil {
			writeJSON(w, 403, map[string]string{"error": "not_operator"})
			return
		}
		target := ""
		method := ""
		switch strings.TrimPrefix(r.URL.Path, "/api/body/task/") {
		case "send":
			target = "/api/send"
			method = http.MethodPost
		case "history":
			target = "/api/history"
			method = http.MethodGet
		case "stop":
			target = "/api/stop"
			method = http.MethodPost
		}
		if target == "" {
			writeJSON(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if r.Method != method {
			writeJSON(w, 405, map[string]string{"error": "method_not_allowed"})
			return
		}
		if method == http.MethodPost && !body.TrustedWriteRequest(r) {
			writeJSON(w, 403, map[string]string{"error": "origin_rejected"})
			return
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Path = target
		u.RawPath = ""
		clone.URL = &u
		api.ServeHTTP(w, clone)
	}))
}
