package bodyui

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"mineagent/internal/account"
	"mineagent/internal/body"
	"mineagent/internal/storage"
)

func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Handler requires a password/passkey-verified administrator and an explicit,
// same-origin confirmation of the exact live Minecraft connection shown in preview.
func (b *Bridge) Handler(acct *account.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail := func(status int, code string) { jsonReply(w, status, map[string]string{"error": code}) }
		if r.Method != http.MethodPost {
			fail(405, "method_not_allowed")
			return
		}
		p := b.core.Admin(r, acct)
		if p == nil || b.core.Check(r.Context(), p, true) != nil {
			fail(403, "not_operator")
			return
		}
		if !b.core.Enabled() {
			fail(404, "body_disabled")
			return
		}
		if !body.TrustedWriteRequest(r) {
			fail(403, "origin_rejected")
			return
		}
		var req struct {
			Action  string `json:"action"`
			Code    string `json:"code"`
			UUID    string `json:"uuid"`
			Session string `json:"session"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF {
			fail(400, "invalid_args")
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		if req.Action == "revoke" {
			for _, v := range b.bindings {
				if v.user == p.UserID && v.authSession == p.AuthSessionID {
					b.revokeLocked(v)
				}
			}
			jsonReply(w, 200, map[string]any{"ok": true})
			return
		}
		if req.Action != "preview" && req.Action != "approve" {
			fail(400, "invalid_action")
			return
		}
		if len(req.Code) != 32 {
			fail(400, "invalid_code")
			return
		}
		var found *binding
		for _, v := range b.bindings {
			if v.allowed && time.Now().Before(v.codeUntil) && subtle.ConstantTimeCompare([]byte(req.Code), []byte(v.code)) == 1 {
				found = v
			}
		}
		if found == nil {
			fail(409, "expired_code")
			return
		}
		if req.Action == "preview" {
			jsonReply(w, 200, map[string]any{"player": found.player, "uuid": found.uuid, "session": found.nonce, "durationSec": 600})
			return
		}
		if req.UUID != found.uuid || req.Session != found.nonce {
			fail(409, "stale_connection")
			return
		}
		if b.store == nil {
			fail(503, "audit_unavailable")
			return
		}
		args, _ := json.Marshal(map[string]any{"player": found.player, "uuid": found.uuid, "durationSec": 600})
		peer := b.peer
		b.mu.Unlock()
		_, err := b.store.SaveAudit(r.Context(), storage.AuditEntry{CallID: body.ID(), Tool: "body_ui_link", Risk: "body:controller", Requester: fmt.Sprintf("web:%d", p.UserID), Operator: p.Name, Args: string(args), Decision: "approved", CreatedAt: time.Now().UnixMilli()})
		b.mu.Lock()
		if err != nil {
			fail(503, "audit_unavailable")
			return
		}
		if b.peer != peer || b.bindings[req.UUID] != found || !found.allowed || found.code != req.Code || time.Now().After(found.codeUntil) {
			fail(409, "stale_connection")
			return
		}
		b.revokeLocked(found)
		found.user = p.UserID
		found.authSession = p.AuthSessionID
		found.admin = p.Name
		found.until = time.Now().Add(10 * time.Minute)
		jsonReply(w, 200, map[string]any{"ok": true, "expiresAt": found.until.UnixMilli()})
	})
}
