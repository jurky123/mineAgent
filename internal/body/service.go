// Package body connects a single optional Minecraft body to the existing agent.
package body

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"mineagent/internal/config"
	"mineagent/internal/protocol"
	"mineagent/internal/storage"
	"mineagent/internal/tools"
)

type Principal struct {
	UserID, AuthSessionID int64
	Name, RunID           string
	Epoch                 uint64
	Deadline              time.Time
	mu                    sync.Mutex
	failures              int
}
type principalKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

type pendingAction struct {
	spec      protocol.BodySpec
	principal *Principal
	args      protocol.BodyArgs
	callID    string
}
type Service struct {
	cfg                           config.Body
	log                           *slog.Logger
	store                         *storage.Store
	approvals                     *tools.Approvals
	authorize                     func(context.Context, int64, int64) bool
	verifyWorld                   func(context.Context, protocol.BodySpec) error
	mu                            sync.Mutex
	peer                          tools.Sender
	connection                    string
	epoch, revision               uint64
	ready, paused, draining, busy bool
	running                       *Principal
	capabilities                  map[string]bool
	state                         protocol.BodyState
	received                      time.Time
	calls                         map[string]chan protocol.BodyResult
	inspections                   map[string]chan protocol.BodyInspectResult
	controls                      map[string]chan protocol.BodyAck
	waiting                       map[string]pendingAction
	events                        []protocol.BodyEvent
	subs                          map[chan struct{}]struct{}
	limits                        map[string][]time.Time
}

func New(cfg config.Body, store *storage.Store, approvals *tools.Approvals, log *slog.Logger) *Service {
	return &Service{cfg: cfg, store: store, approvals: approvals, log: log, paused: true, revision: 1, epoch: 1, capabilities: map[string]bool{}, calls: map[string]chan protocol.BodyResult{}, inspections: map[string]chan protocol.BodyInspectResult{}, controls: map[string]chan protocol.BodyAck{}, waiting: map[string]pendingAction{}, subs: map[chan struct{}]struct{}{}, limits: map[string][]time.Time{}}
}
func (s *Service) WithAuthorization(fn func(context.Context, int64, int64) bool) { s.authorize = fn }
func (s *Service) WithWorldVerifier(fn func(context.Context, protocol.BodySpec) error) {
	s.verifyWorld = fn
}
func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (s *Service) Principal(userID, sessionID int64, name string) *Principal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &Principal{UserID: userID, AuthSessionID: sessionID, Name: name, RunID: ID(), Epoch: s.epoch, Deadline: time.Now().Add(time.Duration(s.cfg.RunTimeoutSec) * time.Second)}
}
func (s *Service) Enabled() bool { return s != nil && s.cfg.Enabled && s.cfg.Mode != "off" }
func (s *Service) Check(ctx context.Context, p *Principal, control bool) error {
	if p == nil || p.UserID == 0 || p.AuthSessionID == 0 || s.authorize == nil || !s.authorize(ctx, p.UserID, p.AuthSessionID) {
		return errors.New("not_operator")
	}
	if control {
		return nil
	}
	if time.Now().After(p.Deadline) {
		return errors.New("timeout")
	}
	p.mu.Lock()
	failed := p.failures >= 3
	p.mu.Unlock()
	if failed {
		return errors.New("run_failed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Enabled() {
		return errors.New("unsupported")
	}
	if s.epoch != p.Epoch {
		return errors.New("stale")
	}
	return nil
}
func (s *Service) Attached(peer tools.Sender) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peer == peer
}
func (s *Service) Attach(peer tools.Sender, hello protocol.Hello) error {
	s.mu.Lock()
	if s.peer != nil {
		s.mu.Unlock()
		return errors.New("body already connected")
	}
	if !s.Enabled() || hello.BodyID != s.cfg.ID || hello.BodyName != s.cfg.Name || hello.BodyProtocolVersion != protocol.BodyVersion || hello.TargetMcVersion != s.cfg.ExpectedMcVersion {
		s.mu.Unlock()
		return errors.New("body incompatible")
	}
	s.peer = peer
	s.connection = ID()
	s.epoch++
	s.ready = false
	s.paused = true
	s.received = time.Time{}
	s.state = protocol.BodyState{}
	s.capabilities = map[string]bool{}
	for _, c := range hello.Capabilities {
		s.capabilities[c] = true
	}
	policy := s.policyLocked()
	s.mu.Unlock()
	if err := peer.SendProtocol(protocol.TypeBodyPolicy, policy); err != nil {
		s.Detach(peer)
		return err
	}
	s.notify()
	return nil
}
func (s *Service) policyLocked() map[string]any {
	return map[string]any{"connectionId": s.connection, "controlEpoch": s.epoch, "policyRevision": s.revision, "bodyId": s.cfg.ID, "name": s.cfg.Name, "mode": s.cfg.Mode, "paused": true, "worldMappings": s.cfg.WorldMappings, "zones": s.cfg.Zones, "denyBlocks": s.cfg.DenyBlocks, "denyItems": s.cfg.DenyItems, "highRiskItems": s.cfg.HighRiskItems, "loopGuardCommands": s.cfg.LoopGuardCommands, "limits": s.cfg.Limits, "expectedMcVersion": s.cfg.ExpectedMcVersion}
}
func (s *Service) Detach(peer tools.Sender) {
	s.mu.Lock()
	if s.peer != peer {
		s.mu.Unlock()
		return
	}
	s.peer = nil
	s.ready = false
	s.paused = true
	s.epoch++
	s.received = time.Time{}
	s.waiting = map[string]pendingAction{}
	for id, ch := range s.calls {
		select {
		case ch <- failure(id, "outcome_unknown", "unknown"):
		default:
		}
	}
	for id, ch := range s.inspections {
		select {
		case ch <- protocol.BodyInspectResult{InspectID: id, Error: "body_offline", Code: "body_offline"}:
		default:
		}
	}
	s.mu.Unlock()
	s.invalidateApprovals()
	s.notify()
}
func failure(id, code, outcome string) protocol.BodyResult {
	return protocol.BodyResult{CallID: id, Code: code, Error: code, Outcome: outcome, Summary: code}
}
func (s *Service) Run(ctx context.Context) {
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cleanup.C:
			if s.store != nil {
				go func() { _ = s.store.CleanupBodyAudits(ctx) }()
			}
		case <-tick.C:
			s.mu.Lock()
			peer := s.peer
			principal := s.running
			for id, w := range s.waiting {
				if w.spec.ExpiresAt <= time.Now().UnixMilli() {
					delete(s.waiting, id)
				}
			}
			lease := map[string]any{"connectionId": s.connection, "controlEpoch": s.epoch, "leaseMs": 1500}
			s.mu.Unlock()
			if principal != nil && s.authorize != nil && !s.authorize(ctx, principal.UserID, principal.AuthSessionID) {
				s.pauseUnknown()
				continue
			}
			if peer != nil {
				if err := peer.SendProtocol(protocol.TypeBodyLease, lease); err != nil {
					s.Detach(peer)
				}
			}
		}
	}
}
func finitePos(p [3]float64) bool {
	for _, v := range p {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 30000000 {
			return false
		}
	}
	return true
}
func (s *Service) Handle(peer tools.Sender, env *protocol.Envelope) {
	if len(env.Data) > 16<<10 {
		return
	}
	s.mu.Lock()
	if s.peer != peer {
		s.mu.Unlock()
		return
	}
	changed := false
	switch env.Type {
	case protocol.TypeBodyPolicyAck:
		var a protocol.BodyAck
		if env.Decode(&a) == nil && a.ConnectionID == s.connection && a.PolicyRevision == s.revision && a.ControlEpoch == s.epoch {
			s.ready = true
			changed = true
		}
	case protocol.TypeBodyControlAck:
		var a protocol.BodyAck
		if env.Decode(&a) == nil && a.ConnectionID == s.connection && a.ControlEpoch == s.epoch {
			if ch := s.controls[a.ID]; ch != nil {
				select {
				case ch <- a:
				default:
				}
			}
		}
	case protocol.TypeBodyState:
		var st protocol.BodyState
		if env.Decode(&st) == nil && st.ConnectionID == s.connection && st.ControlEpoch == s.epoch && st.PolicyRevision == s.revision && st.Seq > s.state.Seq && st.Name == s.cfg.Name && finitePos(st.Pos) {
			s.state = st
			s.received = time.Now()
			if st.Paused {
				s.paused = true
			}
			changed = true
		}
	case protocol.TypeBodyEvent:
		var e protocol.BodyEvent
		if env.Decode(&e) == nil && e.ConnectionID == s.connection && len(e.Data) <= 2048 && len(e.Type) <= 40 {
			if len(s.events) >= 200 {
				s.events = s.events[1:]
			}
			s.events = append(s.events, e)
			changed = true
		}
	case protocol.TypeBodyResult:
		var r protocol.BodyResult
		if env.Decode(&r) == nil && r.ConnectionID == s.connection {
			if ch := s.calls[r.CallID]; ch != nil {
				select {
				case ch <- r:
				default:
				}
			}
		}
	case protocol.TypeBodyInspectResult:
		var r protocol.BodyInspectResult
		if env.Decode(&r) == nil && r.ConnectionID == s.connection {
			if ch := s.inspections[r.InspectID]; ch != nil {
				select {
				case ch <- r:
				default:
				}
			}
		}
	}
	s.mu.Unlock()
	if changed {
		s.notify()
	}
}
func (s *Service) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
func (s *Service) Snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"enabled": s.Enabled(), "mode": s.cfg.Mode, "bodyId": s.cfg.ID, "name": s.cfg.Name, "controlConnected": s.peer != nil, "ready": s.ready, "paused": s.paused, "draining": s.draining, "busy": s.busy, "controlEpoch": s.epoch, "state": s.state, "stale": s.received.IsZero() || time.Since(s.received) > 10*time.Second, "receivedAt": s.received.UnixMilli(), "pendingApprovals": len(s.waiting), "events": append([]protocol.BodyEvent{}, s.events...)}
}
func (s *Service) audit(ctx context.Context, p *Principal, id, tool, decision, operator string, data any) error {
	if s.store == nil {
		return errors.New("audit_unavailable")
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	if r, ok := data.(protocol.BodyResult); ok {
		data = map[string]any{"ok": r.OK, "outcome": r.Outcome, "code": r.Code, "elapsedMs": r.ElapsedMS}
	}
	b, _ := json.Marshal(data)
	var value any
	if json.Unmarshal(b, &value) == nil {
		maskAudit(value)
		b, _ = json.Marshal(value)
	}
	if len(b) > 4000 {
		b = []byte(`{"truncated":true}`)
	}
	requester := "system"
	if p != nil {
		requester = fmt.Sprintf("web:%d", p.UserID)
	}
	_, err := s.store.SaveAudit(auditCtx, storage.AuditEntry{SessionID: tools.SessionFromContext(ctx), CallID: id, Tool: tool, Risk: "body:" + category(tool), Requester: requester, Args: string(b), Decision: decision, Operator: operator, CreatedAt: time.Now().UnixMilli()})
	return err
}
func (s *Service) acquire(tool string, p *Principal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Epoch != s.epoch {
		return errors.New("stale")
	}
	if s.peer == nil || !s.ready || !s.state.Connected || s.received.IsZero() || time.Since(s.received) > 10*time.Second {
		return errors.New("body_offline")
	}
	if s.draining {
		return errors.New("body_draining")
	}
	if !isRead(tool) && (s.paused || s.state.Paused) {
		return errors.New("body_paused")
	}
	if !isRead(tool) && s.cfg.Mode != "assist" {
		return errors.New("body_observe_only")
	}
	if !s.capabilities[capability(tool)] {
		return errors.New("unsupported")
	}
	if s.busy {
		return errors.New("busy")
	}
	if !s.limitLocked("all", 120) || (!isRead(tool) && !s.limitLocked("actions", s.cfg.Limits.ActionsPerMin)) {
		return errors.New("rate_limited")
	}
	key, n := "", 0
	switch category(tool) {
	case "build":
		key, n = "blocks", s.cfg.Limits.BlocksPerMin
	case "chat":
		key, n = "chat", s.cfg.Limits.ChatPerMin
	case "command":
		key, n = "commands", s.cfg.Limits.CommandsPerMin
	}
	if key != "" && !s.limitLocked(key, n) {
		return errors.New("rate_limited")
	}
	s.busy = true
	s.running = p
	return nil
}
func (s *Service) limitLocked(key string, n int) bool {
	now := time.Now()
	old := s.limits[key]
	i := 0
	for i < len(old) && now.Sub(old[i]) >= time.Minute {
		i++
	}
	old = old[i:]
	if len(old) >= n {
		s.limits[key] = old
		return false
	}
	s.limits[key] = append(old, now)
	return true
}
func (s *Service) release() { s.mu.Lock(); s.busy = false; s.running = nil; s.mu.Unlock(); s.notify() }
func (s *Service) inspect(ctx context.Context, p *Principal, tool string, args protocol.BodyArgs) (protocol.BodySpec, error) {
	s.mu.Lock()
	id := ID()
	ch := make(chan protocol.BodyInspectResult, 1)
	s.inspections[id] = ch
	peer := s.peer
	req := protocol.BodyInspect{InspectID: id, ConnectionID: s.connection, ControlEpoch: s.epoch, PolicyRevision: s.revision, Tool: tool, Args: args, ExpiresAt: p.Deadline.UnixMilli()}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.inspections, id); s.mu.Unlock() }()
	if peer == nil {
		return protocol.BodySpec{}, errors.New("body_offline")
	}
	if err := peer.SendProtocol(protocol.TypeBodyInspect, req); err != nil {
		return protocol.BodySpec{}, err
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return protocol.BodySpec{}, ctx.Err()
	case <-timer.C:
		return protocol.BodySpec{}, errors.New("inspect_timeout")
	case r := <-ch:
		if !r.OK || r.Spec == nil {
			return protocol.BodySpec{}, fmt.Errorf("%s: %s", r.Code, r.Error)
		}
		sp := *r.Spec
		if sp.Tool != tool || sp.ConnectionID != req.ConnectionID || sp.ControlEpoch != req.ControlEpoch || sp.PolicyRevision != req.PolicyRevision || sp.ID == "" || !finitePos(sp.Pos) || sp.ExpiresAt > p.Deadline.UnixMilli() || sp.ExpiresAt <= time.Now().UnixMilli() {
			return protocol.BodySpec{}, errors.New("invalid_spec")
		}
		if err := s.policy(sp); err != nil {
			return protocol.BodySpec{}, err
		}
		if s.verifyWorld == nil {
			return protocol.BodySpec{}, errors.New("world_unknown")
		}
		if err := s.verifyWorld(ctx, sp); err != nil {
			return protocol.BodySpec{}, err
		}
		return sp, nil
	}
}
func (s *Service) execute(ctx context.Context, p *Principal, tool string, args protocol.BodyArgs, id string, sp *protocol.BodySpec, grant *protocol.BodyGrant) protocol.BodyResult {
	if err := s.Check(ctx, p, false); err != nil {
		return failure(id, err.Error(), "not_executed")
	}
	ms := timeoutMS(tool, args)
	if time.Until(p.Deadline) < time.Duration(ms+2000)*time.Millisecond {
		return failure(id, "timeout", "not_executed")
	}
	s.mu.Lock()
	peer := s.peer
	ch := make(chan protocol.BodyResult, 1)
	s.calls[id] = ch
	req := protocol.BodyCall{CallID: id, ConnectionID: s.connection, ControlEpoch: s.epoch, PolicyRevision: s.revision, Tool: tool, Args: args, TimeoutMS: ms, Grant: grant}
	if sp != nil {
		req.SpecID = sp.ID
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.calls, id); s.mu.Unlock() }()
	if !isRead(tool) {
		if err := s.audit(ctx, p, id, tool, "dispatch", "", map[string]any{"spec": sp, "runId": p.RunID}); err != nil {
			return failure(id, "audit_unavailable", "not_executed")
		}
	}
	if peer == nil {
		return failure(id, "body_offline", "not_executed")
	}
	if err := peer.SendProtocol(protocol.TypeBodyCall, req); err != nil {
		s.pauseUnknown()
		return failure(id, "outcome_unknown", "unknown")
	}
	timer := time.NewTimer(time.Duration(ms+2000) * time.Millisecond)
	defer timer.Stop()
	var res protocol.BodyResult
	select {
	case <-ctx.Done():
		res = failure(id, "outcome_unknown", "unknown")
	case <-timer.C:
		res = failure(id, "outcome_unknown", "unknown")
	case res = <-ch:
	}
	if res.Outcome == "unknown" {
		s.pauseUnknown()
	} else if res.ControlEpoch != 0 && res.ControlEpoch != req.ControlEpoch {
		res = failure(id, "stale", "unknown")
		s.pauseUnknown()
	}
	if res.Outcome != "confirmed" && res.Outcome != "submitted" && res.Outcome != "unknown" && res.Outcome != "not_executed" {
		res = failure(id, "outcome_unknown", "unknown")
		s.pauseUnknown()
	}
	if !res.OK {
		p.mu.Lock()
		p.failures++
		p.mu.Unlock()
	}
	_ = s.audit(ctx, p, id, tool, "result", "", res)
	return res
}
func (s *Service) pauseUnknown() {
	s.mu.Lock()
	paused := s.paused
	s.waiting = map[string]pendingAction{}
	s.mu.Unlock()
	s.invalidateApprovals()
	if paused {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = s.Control(ctx, "stop", nil)
}

func maskAudit(v any) {
	switch m := v.(type) {
	case map[string]any:
		for k, x := range m {
			if k == "command" {
				if text, ok := x.(string); ok {
					f := strings.Fields(text)
					if len(f) > 0 {
						m[k] = f[0] + " <arguments redacted>"
					}
				}
			} else {
				maskAudit(x)
			}
		}
	case []any:
		for _, x := range m {
			maskAudit(x)
		}
	}
}
