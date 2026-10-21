package body

import (
	"context"
	"errors"
	"mineagent/internal/protocol"
	"time"
)

func (s *Service) invalidateApprovals() {
	if s.approvals != nil {
		s.approvals.CancelBody("身体控制状态已变更")
	}
}

// Control bypasses the action lock and rate limits. nil principal is reserved for internal fail-closed cancellation.
func (s *Service) Control(ctx context.Context, action string, p *Principal) (protocol.BodyAck, error) {
	if p != nil {
		if err := s.Check(ctx, p, true); err != nil {
			return protocol.BodyAck{}, err
		}
	}
	if action != "stop" && action != "pause" && action != "resume" && action != "disconnect" {
		return protocol.BodyAck{}, errors.New("invalid_action")
	}
	s.mu.Lock()
	if action == "resume" && (s.peer == nil || !s.ready || s.busy) {
		s.mu.Unlock()
		return protocol.BodyAck{}, errors.New("body_not_ready")
	}
	if action == "resume" && !s.paused && !s.draining && !s.state.Paused {
		ack := protocol.BodyAck{ConnectionID: s.connection, ControlEpoch: s.epoch, PolicyRevision: s.revision, Paused: false}
		s.mu.Unlock()
		return ack, nil
	}
	s.resumeFromEpoch = 0
	if action == "pause" {
		s.pauseFromEpoch = s.epoch
	} else if action != "resume" {
		s.pauseFromEpoch = 0
	}
	if action == "resume" {
		s.resumeFromEpoch = s.epoch
	}
	s.epoch++
	if action == "stop" || action == "disconnect" {
		s.emergencyEpoch = s.epoch
	}
	s.paused = true
	s.waiting = map[string]pendingAction{}
	if action == "resume" {
		s.draining = false
	}
	id := ID()
	peer := s.peer
	ch := make(chan protocol.BodyAck, 1)
	s.controls[id] = ch
	req := protocol.BodyControl{ID: id, ConnectionID: s.connection, ControlEpoch: s.epoch, Action: action}
	s.mu.Unlock()
	s.invalidateApprovals()
	s.notify()
	defer func() { s.mu.Lock(); delete(s.controls, id); s.mu.Unlock() }()
	go func() {
		_ = s.audit(ctx, p, id, "body_control", "requested", "", map[string]any{"action": action, "epoch": req.ControlEpoch})
	}()
	if peer == nil {
		return protocol.BodyAck{}, errors.New("stop_unconfirmed")
	}
	if err := peer.SendProtocol(protocol.TypeBodyControl, req); err != nil {
		return protocol.BodyAck{}, errors.New("stop_unconfirmed")
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return protocol.BodyAck{}, errors.New("stop_unconfirmed")
	case <-timer.C:
		return protocol.BodyAck{}, errors.New("stop_unconfirmed")
	case ack := <-ch:
		s.mu.Lock()
		if ack.ControlEpoch == s.epoch {
			s.paused = ack.Paused
		}
		s.mu.Unlock()
		s.notify()
		go func() { _ = s.audit(ctx, p, id, "body_control", "confirmed", "", ack) }()
		return ack, nil
	}
}

// CancelAction stops one currently dispatched action, without changing run
// authority, latching pause or reviving a paused body. The target ID prevents a
// delayed cancel from touching a different subsequent action.
func (s *Service) CancelAction(ctx context.Context, p *Principal) (protocol.BodyAck, error) {
	if err := s.Check(ctx, p, true); err != nil {
		return protocol.BodyAck{}, err
	}
	s.mu.Lock()
	id, peer := ID(), s.peer
	ch := make(chan protocol.BodyAck, 1)
	s.controls[id] = ch
	req := protocol.BodyControl{ID: id, ConnectionID: s.connection, ControlEpoch: s.epoch, Action: "cancel", CallID: s.activeCallID}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.controls, id); s.mu.Unlock() }()
	if err := s.audit(ctx, p, id, "body_stop", "requested", "", map[string]any{"action": "cancel", "callId": req.CallID}); err != nil {
		return protocol.BodyAck{}, errors.New("audit_unavailable")
	}
	if peer == nil || peer.SendProtocol(protocol.TypeBodyControl, req) != nil {
		s.pauseUnknown()
		return protocol.BodyAck{}, errors.New("stop_unconfirmed")
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case ack := <-ch:
		return ack, nil
	case <-ctx.Done():
		s.pauseUnknown()
		return protocol.BodyAck{}, errors.New("stop_unconfirmed")
	case <-timer.C:
		s.pauseUnknown()
		return protocol.BodyAck{}, errors.New("stop_unconfirmed")
	}
}
func (s *Service) Drain(ctx context.Context, p *Principal) error {
	if err := s.Check(ctx, p, true); err != nil {
		return err
	}
	s.mu.Lock()
	s.draining = true
	s.waiting = map[string]pendingAction{}
	s.mu.Unlock()
	s.invalidateApprovals()
	s.notify()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		idle := !s.busy && s.state.Task == nil && s.peer != nil && s.ready && !s.received.IsZero() && time.Since(s.received) < 10*time.Second
		s.mu.Unlock()
		if idle {
			return s.audit(ctx, p, ID(), "body_drain", "confirmed", "", map[string]bool{"idle": true})
		}
		select {
		case <-ctx.Done():
			return errors.New("body_drain_timeout")
		case <-ticker.C:
		}
	}
}
func (s *Service) Profile() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	stale := s.received.IsZero() || time.Since(s.received) > 10*time.Second || s.peer == nil
	status, activity := "unknown", "unknown"
	var since *int64
	var dim any
	if !stale {
		if !s.state.Connected {
			status = "offline"
		} else {
			status = "online"
			activity = "idle"
			since = s.state.OnlineSince
			if !s.cfg.Profile.HideDimension {
				dim = s.state.Dimension
			}
			if s.state.Task != nil {
				status = "busy"
				activity = activityOf(s.state.Task.Tool)
			}
		}
	}
	caps := []string{}
	for _, entry := range []struct{ wire, display string }{{"observe", "observe"}, {"move", "move"}, {"dig", "build"}, {"chat", "chat"}, {"command", "command"}} {
		if s.capabilities[entry.wire] {
			caps = append(caps, entry.display)
		}
	}
	updated := int64(0)
	if !s.received.IsZero() {
		updated = s.received.UnixMilli()
	}
	var skin any
	if s.cfg.Profile.Skin != "" {
		skin = s.cfg.Profile.Skin
	}
	display := s.cfg.Profile.DisplayName
	if display == "" {
		display = s.cfg.Name
	}
	return map[string]any{"profileVersion": 1, "id": s.cfg.ID, "name": s.cfg.Name, "displayName": display, "skin": skin, "status": status, "activity": activity, "dimension": dim, "onlineSince": since, "updatedAt": updated, "stale": stale, "capabilities": caps}
}
func activityOf(t string) string {
	switch t {
	case "body_goto":
		return "moving"
	case "body_dig":
		return "digging"
	case "body_place":
		return "placing"
	case "body_interact":
		return "interacting"
	case "body_use_item":
		return "using_item"
	case "body_say":
		return "chatting"
	case "body_command":
		return "command"
	default:
		return "unknown"
	}
}
