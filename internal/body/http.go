package body

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mineagent/internal/account"
)

func tokenOf(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie("mineagent_token"); err == nil {
		return c.Value
	}
	return ""
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func errorResponse(w http.ResponseWriter, status int, code string) {
	jsonResponse(w, status, map[string]string{"error": code, "code": code})
}
func (s *Service) Admin(r *http.Request, acct *account.Service) *Principal {
	if r.URL.Query().Get("token") != "" {
		return nil
	}
	u, sid, ok := acct.CurrentWithSession(r.Context(), tokenOf(r))
	if !ok || !u.IsAdmin {
		return nil
	}
	return s.Principal(u.ID, sid, u.Username)
}
func TrustedWriteRequest(r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && media == "application/json" && r.URL.Query().Get("token") == "" && sameOrigin(r)
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	u, err := url.Parse(origin)
	if err != nil || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return u.Scheme == scheme && strings.EqualFold(u.Host, r.Host)
}
func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		errorResponse(w, 415, "json_required")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		errorResponse(w, 400, "invalid_args")
		return false
	}
	return true
}
func (s *Service) Handler(acct *account.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/body/profile" {
			s.handleProfile(w, r, acct)
			return
		}
		p := s.Admin(r, acct)
		if p == nil {
			errorResponse(w, 403, "not_operator")
			return
		}
		if !s.Enabled() {
			errorResponse(w, 404, "body_disabled")
			return
		}
		switch r.URL.Path {
		case "/api/body/status":
			if r.Method != http.MethodGet {
				errorResponse(w, 405, "method_not_allowed")
				return
			}
			jsonResponse(w, 200, s.Snapshot())
			return
		case "/api/body/events":
			if r.Method != http.MethodGet {
				errorResponse(w, 405, "method_not_allowed")
				return
			}
			s.serveEvents(w, r, p)
			return
		case "/api/body/audit":
			if r.Method != http.MethodGet {
				errorResponse(w, 405, "method_not_allowed")
				return
			}
			rows, err := s.store.BodyAudits(r.Context(), 50)
			if err != nil {
				errorResponse(w, 503, "audit_unavailable")
				return
			}
			jsonResponse(w, 200, map[string]any{"entries": rows})
			return
		case "/api/body/approvals":
			if r.Method != http.MethodGet {
				errorResponse(w, 405, "method_not_allowed")
				return
			}
			jsonResponse(w, 200, map[string]any{"approvals": s.Pending()})
			return
		}
		if r.Method != http.MethodPost {
			errorResponse(w, 405, "method_not_allowed")
			return
		}
		if !sameOrigin(r) {
			errorResponse(w, 403, "origin_rejected")
			return
		}
		switch {
		case r.URL.Path == "/api/body/control":
			var b struct {
				Action string `json:"action"`
			}
			if !readBody(w, r, &b) {
				return
			}
			ack, err := s.Control(r.Context(), b.Action, p)
			if err != nil {
				errorResponse(w, 409, ErrorCode(err))
				return
			}
			jsonResponse(w, 200, map[string]any{"ok": true, "ack": ack})
		case r.URL.Path == "/api/body/drain":
			var b struct{}
			if !readBody(w, r, &b) {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			if err := s.Drain(ctx, p); err != nil {
				errorResponse(w, 409, ErrorCode(err))
				return
			}
			jsonResponse(w, 200, map[string]bool{"idle": true, "draining": true})
		case strings.HasPrefix(r.URL.Path, "/api/body/approvals/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/body/approvals/")
			var b struct {
				Approved *bool `json:"approved"`
			}
			if !readBody(w, r, &b) {
				return
			}
			if b.Approved == nil || !s.Decide(r.Context(), p, id, *b.Approved) {
				errorResponse(w, 409, "stale_approval")
				return
			}
			jsonResponse(w, 200, map[string]bool{"ok": true})
		default:
			errorResponse(w, 404, "not_found")
		}
	})
}
func (s *Service) handleProfile(w http.ResponseWriter, r *http.Request, acct *account.Service) {
	if !s.cfg.Profile.Enabled || !s.Enabled() {
		errorResponse(w, 404, "not_found")
		return
	}
	if r.Method != http.MethodGet {
		errorResponse(w, 405, "method_not_allowed")
		return
	}
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	sum := sha256.Sum256([]byte(raw))
	expected, err := hex.DecodeString(s.cfg.Profile.TokenHash)
	authorized := strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && err == nil && len(expected) == 32 && subtle.ConstantTimeCompare(sum[:], expected) == 1
	if !authorized && s.Admin(r, acct) == nil {
		errorResponse(w, 401, "unauthorized")
		return
	}
	s.mu.Lock()
	allowed := s.limitLocked("profile", 120)
	s.mu.Unlock()
	if !allowed {
		w.Header().Set("Retry-After", "5")
		errorResponse(w, 429, "rate_limited")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	jsonResponse(w, 200, s.Profile())
	w.Header().Set("Cache-Control", "private, no-store")
}
func (s *Service) serveEvents(w http.ResponseWriter, r *http.Request, p *Principal) {
	f, ok := w.(http.Flusher)
	if !ok {
		errorResponse(w, 500, "stream_unavailable")
		return
	}
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	if len(s.subs) >= 16 {
		s.mu.Unlock()
		errorResponse(w, 429, "too_many_streams")
		return
	}
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.subs, ch); s.mu.Unlock() }()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func() bool {
		if s.Check(r.Context(), p, true) != nil {
			return false
		}
		b, _ := json.Marshal(s.Snapshot())
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, err := fmt.Fprintf(w, "event: body\ndata: %s\n\n", b)
		f.Flush()
		return err == nil
	}
	if !send() {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !send() {
				return
			}
		case <-ch:
			if !send() {
				return
			}
		}
	}
}
