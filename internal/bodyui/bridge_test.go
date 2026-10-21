package bodyui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mineagent/internal/account"
	"mineagent/internal/body"
	"mineagent/internal/config"
	"mineagent/internal/protocol"
	"mineagent/internal/storage"
)

type testPeer struct {
	replies chan map[string]any
	send    func(string, any) error
}

func (p *testPeer) SendProtocol(typ string, v any) error {
	if p.send != nil {
		return p.send(typ, v)
	}
	raw, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	p.replies <- out
	return nil
}
func wire(typ string, v any) *protocol.Envelope { e, _ := protocol.New(typ, v); return e }

type fixture struct {
	b        *Bridge
	acct     *account.Service
	peer     *testPeer
	token    string
	seq      uint64
	controls atomic.Int32
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultBody()
	cfg.Enabled = true
	cfg.Mode = "assist"
	core := body.New(cfg, store, nil, log)
	f := &fixture{peer: &testPeer{replies: make(chan map[string]any, 20)}, token: "verified-admin-fixture"}
	f.acct = account.New(store, log, nil, []string{"admin"})
	u, err := store.EnsureUser(ctx, "admin", time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetInitialPassword(ctx, u.ID, "fixture-password-hash"); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(f.token))
	hash := hex.EncodeToString(h[:])
	if err = store.CreateAuthSession(ctx, storage.AuthSession{UserID: u.ID, TokenHash: hash, ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkPasswordSession(ctx, hash, "fixture-password-hash"); err != nil {
		t.Fatal(err)
	}
	core.WithAuthorization(f.acct.RecheckAdminSession)
	node := &testPeer{}
	node.send = func(typ string, v any) error {
		switch typ {
		case protocol.TypeBodyPolicy:
			p := v.(map[string]any)
			cid := p["connectionId"].(string)
			epoch := p["controlEpoch"].(uint64)
			core.Handle(node, wire(protocol.TypeBodyPolicyAck, protocol.BodyAck{ConnectionID: cid, ControlEpoch: epoch, PolicyRevision: 1}))
			core.Handle(node, wire(protocol.TypeBodyState, protocol.BodyState{Name: cfg.Name, ConnectionID: cid, ControlEpoch: epoch, PolicyRevision: 1, Seq: 1, Connected: true, WorldID: "private-world", Pos: [3]float64{123, 64, 456}}))
		case protocol.TypeBodyControl:
			f.controls.Add(1)
			c := v.(protocol.BodyControl)
			core.Handle(node, wire(protocol.TypeBodyControlAck, protocol.BodyAck{ID: c.ID, ConnectionID: c.ConnectionID, ControlEpoch: c.ControlEpoch, Paused: c.Action != "resume"}))
		}
		return nil
	}
	if err = core.Attach(node, protocol.Hello{BodyID: cfg.ID, BodyName: cfg.Name, BodyProtocolVersion: 1, TargetMcVersion: cfg.ExpectedMcVersion}); err != nil {
		t.Fatal(err)
	}
	f.b = New(core, store, nil, nil, 12, "https://example.com")
	f.b.Attach(f.peer)
	return f
}

const playerUUID = "12345678-1234-1234-1234-123456789abc"
const gameNonce = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func (f *fixture) request(t *testing.T, action string, allowed bool) map[string]any {
	t.Helper()
	f.seq++
	f.b.Handle(context.Background(), f.peer, wire(RequestType, Request{ID: body.ID(), UUID: playerUUID, Session: gameNonce, Player: "TestPlayer", Allowed: allowed, Seq: f.seq, Action: action}))
	select {
	case r := <-f.peer.replies:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("no UI response")
		return nil
	}
}
func (f *fixture) post(t *testing.T, token, origin string, v any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(v)
	r := httptest.NewRequest("POST", "http://example.com/api/body/minecraft/link", strings.NewReader(string(raw)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.b.Handler(f.acct).ServeHTTP(w, r)
	return w
}
func (f *fixture) link(t *testing.T) {
	t.Helper()
	// Advance only this fixture's pair rate-limit clock; keep expiry assertions deterministic.
	f.b.mu.Lock()
	if v := f.b.bindings[playerUUID]; v != nil {
		v.lastPair = time.Time{}
	}
	f.b.mu.Unlock()
	r := f.request(t, "pair", true)
	data := r["data"].(map[string]any)
	code := data["pairCode"].(string)
	w := f.post(t, f.token, "http://example.com", map[string]any{"action": "approve", "code": code, "uuid": playerUUID, "session": gameNonce})
	if w.Code != 200 {
		t.Fatalf("link: %d %s", w.Code, w.Body.String())
	}
}

func TestPublicStatusNeverGrantsBodyControl(t *testing.T) {
	f := setup(t)
	r := f.request(t, "status", false)
	data := r["data"].(map[string]any)
	if data["linked"] != false || data["snapshot"] != nil || data["approvals"] != nil || data["messages"] != nil {
		t.Fatal("unverified full state exposed", data)
	}
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "private-world") || strings.Contains(string(raw), "123,64,456") {
		t.Fatal("position leaked")
	}
	r = f.request(t, "stop", true)
	if r["error"] != "link_required" || f.controls.Load() != 0 {
		t.Fatal("OP or MC name granted control", r)
	}
	r = f.request(t, "pair", false)
	if r["error"] != "permission_denied" {
		t.Fatal("permission not checked", r)
	}
}

func TestVerifiedPairingCSRFNonceAndOneUse(t *testing.T) {
	f := setup(t)
	r := f.request(t, "pair", true)
	code := r["data"].(map[string]any)["pairCode"].(string)
	preview := map[string]any{"action": "preview", "code": code}
	if w := f.post(t, "name-only-fixture", "http://example.com", preview); w.Code != 403 {
		t.Fatal("unverified identity accepted")
	}
	if w := f.post(t, f.token, "http://evil.example", preview); w.Code != 403 {
		t.Fatal("cross-origin accepted")
	}
	if w := f.post(t, f.token, "http://example.com", preview); w.Code != 200 {
		t.Fatal("preview failed", w.Code)
	}
	approve := map[string]any{"action": "approve", "code": code, "uuid": playerUUID, "session": "ffffffff-ffff-ffff-ffff-ffffffffffff"}
	if w := f.post(t, f.token, "http://example.com", approve); w.Code != 409 {
		t.Fatal("different connection accepted")
	}
	approve["session"] = gameNonce
	if w := f.post(t, f.token, "http://example.com", approve); w.Code != 200 {
		t.Fatal("valid link failed", w.Code, w.Body)
	}
	if w := f.post(t, f.token, "http://example.com", approve); w.Code != 409 {
		t.Fatal("code reused")
	}
	r = f.request(t, "status", true)
	if r["data"].(map[string]any)["snapshot"] == nil {
		t.Fatal("linked state missing")
	}
	r = f.request(t, "stop", true)
	if r["error"] != "" || f.controls.Load() != 1 {
		t.Fatal("control failed", r)
	}
}

func TestDelegationCannotSurviveRebindPermissionOrTransport(t *testing.T) {
	f := setup(t)
	f.link(t)
	ctx := context.Background()
	v := f.b.bindings[playerUUID]
	oldReply := &replyChannel{b: f.b, peer: f.peer, v: v, generation: v.generation}
	f.b.record(f.peer, v, "private earlier task", v.generation)
	p, err := f.b.principal(ctx, f.peer, v)
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, "unlink", true)
	if f.b.core.Check(ctx, p, true) == nil {
		t.Fatal("unlinked principal accepted")
	}
	f.link(t)
	if f.b.core.Check(ctx, p, true) == nil {
		t.Fatal("old principal revived after rebind")
	}
	if oldReply.Send(ctx, storage.Message{Text: "late old task reply"}) == nil {
		t.Fatal("old task reply reached new delegation")
	}
	if len(v.messages) != 0 {
		t.Fatal("prior delegation history exposed after rebind")
	}
	p, _ = f.b.principal(ctx, f.peer, v)
	f.request(t, "status", false)
	if f.b.core.Check(ctx, p, true) == nil {
		t.Fatal("permission revocation ignored")
	}
	f.link(t)
	p, _ = f.b.principal(ctx, f.peer, v)
	other := &testPeer{replies: make(chan map[string]any, 1)}
	f.b.Detach(other)
	if f.b.core.Check(ctx, p, true) != nil {
		t.Fatal("unrelated disconnect revoked current controller")
	}
	f.b.Attach(other)
	if f.b.core.Check(ctx, p, true) == nil {
		t.Fatal("transport replacement retained controller")
	}
}

func TestExpiredAndLoggedOutDelegationRejected(t *testing.T) {
	f := setup(t)
	f.link(t)
	ctx := context.Background()
	v := f.b.bindings[playerUUID]
	f.b.mu.Lock()
	v.until = time.Now().Add(-time.Second)
	f.b.mu.Unlock()
	if _, err := f.b.principal(ctx, f.peer, v); err == nil {
		t.Fatal("expired controller accepted")
	}
	f.link(t)
	if err := f.acct.Logout(ctx, f.token); err != nil {
		t.Fatal(err)
	}
	if f.b.valid(ctx, f.peer, v) {
		t.Fatal("web logout did not revoke game control")
	}
}

func TestRequestSequenceAndLiveNonceAreRequired(t *testing.T) {
	f := setup(t)
	f.link(t)
	f.request(t, "stop", true)
	f.b.Handle(context.Background(), f.peer, wire(RequestType, Request{ID: body.ID(), UUID: playerUUID, Session: gameNonce, Player: "TestPlayer", Allowed: true, Seq: f.seq, Action: "stop"}))
	r := <-f.peer.replies
	if r["error"] != "stale_request" || f.controls.Load() != 1 {
		t.Fatal("replayed control executed")
	}
	v := f.b.bindings[playerUUID]
	p, _ := f.b.principal(context.Background(), f.peer, v)
	f.b.Handle(context.Background(), f.peer, wire(RequestType, Request{ID: body.ID(), UUID: playerUUID, Session: "ffffffff-ffff-ffff-ffff-ffffffffffff", Player: "TestPlayer", Allowed: true, Seq: 1, Action: "status"}))
	r = <-f.peer.replies
	if r["data"].(map[string]any)["linked"] != false || f.b.core.Check(context.Background(), p, true) == nil {
		t.Fatal("reconnected same-name player inherited authority")
	}
}

func TestChatRequiresVerifiedCurrentGameDelegation(t *testing.T) {
	f := setup(t)
	c := protocol.ChatMessage{UUID: playerUUID, Player: "TestPlayer", Session: gameNonce}
	if f.b.Chat(context.Background(), f.peer, c, "过来") {
		t.Fatal("unpaired chat got private body tools")
	}
	f.link(t)
	bad := c
	bad.Session = "old-session"
	if f.b.Chat(context.Background(), f.peer, bad, "过来") {
		t.Fatal("old game nonce accepted")
	}
	bad = c
	bad.Player = "Impostor"
	if f.b.Chat(context.Background(), f.peer, bad, "过来") {
		t.Fatal("name mismatch accepted")
	}
	if !f.b.Chat(context.Background(), f.peer, c, "观察") {
		t.Fatal("verified chat not routed")
	}
	reply := <-f.peer.replies
	if reply["target"] != "TestPlayer" {
		t.Fatal("private error was broadcast", reply)
	}
	f.request(t, "status", false)
	if f.b.Chat(context.Background(), f.peer, c, "观察") {
		t.Fatal("permission revoked chat retained body tools")
	}
}
