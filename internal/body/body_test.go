package body

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
	"mineagent/internal/config"
	"mineagent/internal/protocol"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

type fakePeer struct{ send func(string, any) error }

func (f *fakePeer) SendProtocol(t string, v any) error { return f.send(t, v) }
func envelope(typ string, v any) *protocol.Envelope    { e, _ := protocol.New(typ, v); return e }
func setup(t *testing.T) (*Service, *fakePeer, *Principal, *atomic.Bool) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "body.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cfg := config.DefaultBody()
	cfg.Enabled = true
	cfg.Mode = "assist"
	cfg.WorldMappings = []config.BodyWorld{{Dimension: "minecraft:overworld", WorldID: "yard"}}
	cfg.Zones.Areas = []config.BodyArea{{WorldID: "yard", Min: [3]float64{-10, -64, -10}, Max: [3]float64{30, 100, 30}}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ap := tools.NewApprovals(nil, time.Second, log)
	s := New(cfg, store, ap, log)
	allowed := new(atomic.Bool)
	allowed.Store(true)
	s.WithAuthorization(func(context.Context, int64, int64) bool { return allowed.Load() })
	s.WithWorldVerifier(func(context.Context, protocol.BodySpec) error { return nil })
	peer := &fakePeer{}
	peer.send = func(typ string, v any) error {
		switch typ {
		case protocol.TypeBodyPolicy:
			p := v.(map[string]any)
			cid := p["connectionId"].(string)
			epoch := p["controlEpoch"].(uint64)
			s.Handle(peer, envelope(protocol.TypeBodyPolicyAck, protocol.BodyAck{ConnectionID: cid, ControlEpoch: epoch, PolicyRevision: 1}))
			s.Handle(peer, envelope(protocol.TypeBodyState, protocol.BodyState{ConnectionID: cid, ControlEpoch: epoch, PolicyRevision: 1, Seq: 1, Connected: true, Name: cfg.Name, Pos: [3]float64{0, 64, 0}, WorldID: "yard", Dimension: "minecraft:overworld"}))
		case protocol.TypeBodyControl:
			c := v.(protocol.BodyControl)
			paused := c.Action != "resume"
			s.Handle(peer, envelope(protocol.TypeBodyControlAck, protocol.BodyAck{ID: c.ID, ConnectionID: c.ConnectionID, ControlEpoch: c.ControlEpoch, Paused: paused}))
		}
		return nil
	}
	if err := s.Attach(peer, protocol.Hello{BodyID: cfg.ID, BodyName: cfg.Name, BodyProtocolVersion: 1, TargetMcVersion: "26.2", Capabilities: []string{"observe", "move", "dig", "place", "interact", "item", "chat", "command"}}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.paused = false
	s.mu.Unlock()
	p := s.Principal(1, 1, "admin")
	return s, peer, p, allowed
}
func TestControlBypassesBusyAndInvalidatesOldRun(t *testing.T) {
	s, _, p, _ := setup(t)
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()
	start := time.Now()
	ack, err := s.Control(context.Background(), "stop", p)
	if err != nil || !ack.Paused || time.Since(start) > time.Second {
		t.Fatalf("stop blocked: %v %+v", err, ack)
	}
	if err = s.Check(context.Background(), p, false); err == nil || err.Error() != "stale" {
		t.Fatal("old run retained authority")
	}
}
func TestOldConnectionCannotDetachOrOverwriteNewConnection(t *testing.T) {
	s, peer, _, _ := setup(t)
	s.Detach(&fakePeer{})
	if !s.Attached(peer) {
		t.Fatal("stale detach affected current")
	}
	s.Handle(peer, envelope(protocol.TypeBodyState, protocol.BodyState{ConnectionID: "old", ControlEpoch: s.epoch, Seq: 1000, Connected: false, Name: s.cfg.Name}))
	if !s.state.Connected {
		t.Fatal("old snapshot accepted")
	}
	if s.Attach(&fakePeer{}, protocol.Hello{}) == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestPolicyCoversImplicitHandAndWorldBoundary(t *testing.T) {
	s, _, _, _ := setup(t)
	target := [3]float64{1, 64, 1}
	sp := protocol.BodySpec{Tool: "body_interact", WorldID: "yard", Dimension: "minecraft:overworld", Pos: [3]float64{0, 64, 0}, Target: &target, Block: "oak_door", MainHand: "lava_bucket"}
	if !s.highRisk(sp) || s.reason(sp) == nil {
		t.Fatal("implicit high risk hand bypassed")
	}
	sp.WorldID = "other"
	if s.policy(sp) == nil {
		t.Fatal("unknown world allowed")
	}
	sp.WorldID = "yard"
	target[0] = 40
	if s.policy(sp) == nil {
		t.Fatal("out of zone allowed")
	}
	target[0] = 1
	sp.Block = "command_block"
	sp.Args.Reason = "test"
	if s.policy(sp) == nil {
		t.Fatal("hard deny bypassed")
	}
}
func TestRevocationAndDeadlineCheckedBeforeDispatch(t *testing.T) {
	s, peer, p, allowed := setup(t)
	calls := 0
	peer.send = func(string, any) error { calls++; return nil }
	allowed.Store(false)
	r := s.execute(context.Background(), p, "body_say", protocol.BodyArgs{Text: "hello"}, "call", nil, nil)
	if r.OK || calls != 0 || r.Code != "not_operator" {
		t.Fatalf("revoked principal dispatched %+v", r)
	}
	allowed.Store(true)
	p.Deadline = time.Now().Add(-time.Second)
	r = s.execute(context.Background(), p, "body_say", protocol.BodyArgs{Text: "hello"}, "call", nil, nil)
	if calls != 0 || r.Code != "timeout" {
		t.Fatal("expired run dispatched")
	}
}
func TestStopInvalidatesPendingPortalApproval(t *testing.T) {
	s, _, p, _ := setup(t)
	info, err := s.approvals.Create(tools.ApprovalInfo{Source: "body", Tool: "body_place", TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	s.approvals.Notify(info)
	s.waiting[info.ApprovalID] = pendingAction{spec: protocol.BodySpec{ControlEpoch: p.Epoch, ExpiresAt: p.Deadline.UnixMilli()}, principal: p}
	if _, err = s.Control(context.Background(), "stop", p); err != nil {
		t.Fatal(err)
	}
	fresh := s.Principal(1, 1, "admin")
	if s.Decide(context.Background(), fresh, info.ApprovalID, true) || len(s.Pending()) != 0 {
		t.Fatal("stopped approval survived")
	}
}
func TestArgumentsCannotSmuggleCommandsOrDigging(t *testing.T) {
	for _, tc := range []struct{ tool, args string }{{"body_say", `{"text":"/op somebody"}`}, {"body_goto", `{"x":1,"y":64,"z":1,"allowDig":true}`}, {"body_command", `{"command":"minecraft:MA help"}`}, {"body_dig", `{"x":1.5,"y":64,"z":1}`}, {"body_place", `{"x":1,"y":64,"z":1,"reason":"line\nbreak"}`}} {
		if _, err := parseArgs(tc.tool, tc.args); err == nil {
			t.Errorf("allowed %s %s", tc.tool, tc.args)
		}
	}
}
func authFixture(t *testing.T, s *Service) (*account.Service, string) {
	t.Helper()
	ctx := context.Background()
	u, err := s.store.EnsureUser(ctx, "admin", time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.SetInitialPassword(ctx, u.ID, "test-password-hash"); err != nil {
		t.Fatal(err)
	}
	token := "fixture-verified-admin-token"
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	if err = s.store.CreateAuthSession(ctx, storage.AuthSession{UserID: u.ID, TokenHash: hash, CreatedAt: time.Now().UnixMilli(), ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err = s.store.MarkPasswordSession(ctx, hash, "test-password-hash"); err != nil {
		t.Fatal(err)
	}
	acct := account.New(s.store, s.log, nil, []string{"admin"})
	s.WithAuthorization(acct.RecheckAdminSession)
	return acct, token
}
func TestProfileScopeProjectionAndCSRF(t *testing.T) {
	s, _, _, _ := setup(t)
	acct, admin := authFixture(t, s)
	readToken := "fixture-profile-only-token"
	sum := sha256.Sum256([]byte(readToken))
	s.cfg.Profile = config.BodyProfile{Enabled: true, TokenHash: hex.EncodeToString(sum[:])}
	handler := s.Handler(acct)
	request := func(method, path, token, origin, payload string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+token)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "/api/body/profile", readToken, "", "")
	if w.Code != 200 {
		t.Fatalf("profile %d %s", w.Code, w.Body.String())
	}
	var profile map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &profile)
	for _, key := range []string{"position", "worldId", "lastEvent", "task", "requester"} {
		if _, ok := profile[key]; ok {
			t.Error("private field exposed", key)
		}
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Error("profile cache policy")
	}
	for _, path := range []string{"/api/body/status", "/api/body/events", "/api/body/control", "/api/body/approvals"} {
		if request("GET", path, readToken, "", "").Code != 403 {
			t.Error("profile token escalated", path)
		}
	}
	if request("POST", "/api/body/control", admin, "https://evil.example", `{"action":"stop"}`).Code != 403 {
		t.Error("cross origin accepted")
	}
	if request("GET", "/api/body/status?token="+admin, admin, "", "").Code != 403 {
		t.Error("URL token accepted")
	}
}
func TestDrainRejectsNewActionsEvenIfInitiallyBusy(t *testing.T) {
	s, _, p, _ := setup(t)
	s.busy = true
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if s.Drain(ctx, p) == nil {
		t.Fatal("busy drain succeeded")
	}
	if err := s.acquire("body_goto", p); err == nil || err.Error() != "body_draining" {
		t.Fatalf("new action accepted: %v", err)
	}
}
