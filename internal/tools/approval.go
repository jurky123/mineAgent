package tools

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"mineagent/internal/protocol"
)

func init() {
	gob.Register(&ApprovalInfo{})
	gob.Register(&ApprovalDecision{})
}

const defaultMaxPending = 64

type ApprovalDecision struct {
	Approved bool   `json:"approved"`
	Operator string `json:"operator,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type ApprovalInfo struct {
	ApprovalID string          `json:"approvalId"`
	Tool       string          `json:"tool"`
	Args       json.RawMessage `json:"args,omitempty"`
	Requester  string          `json:"requester,omitempty"`
	Prompt     string          `json:"prompt,omitempty"`
}

type Outcome struct {
	ApprovalID string
	Info       ApprovalInfo
	Decision   ApprovalDecision
}

type pendingApproval struct {
	info  ApprovalInfo
	timer *time.Timer
}

type Approvals struct {
	sender     Sender
	log        *slog.Logger
	timeout    time.Duration
	maxPending int

	mu      sync.Mutex
	pending map[string]*pendingApproval
	decided map[string]Outcome
	notify  chan struct{}
	seq     atomic.Uint64
}

func NewApprovals(sender Sender, timeout time.Duration, log *slog.Logger) *Approvals {
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	return &Approvals{
		sender:     sender,
		log:        log,
		timeout:    timeout,
		maxPending: defaultMaxPending,
		pending:    make(map[string]*pendingApproval),
		decided:    make(map[string]Outcome),
		notify:     make(chan struct{}, 1),
	}
}

func (a *Approvals) Decided() <-chan struct{} { return a.notify }

func (a *Approvals) Create(info ApprovalInfo) (ApprovalInfo, error) {
	a.mu.Lock()
	if len(a.pending) >= a.maxPending {
		n := len(a.pending)
		a.mu.Unlock()
		return info, fmt.Errorf("待审批请求过多（%d），请稍后再试", n)
	}
	info.ApprovalID = fmt.Sprintf("ap-%d-%d", time.Now().UnixMilli(), a.seq.Add(1))
	a.pending[info.ApprovalID] = &pendingApproval{info: info}
	a.mu.Unlock()
	a.log.Info("approval created", "approvalId", info.ApprovalID, "tool", info.Tool, "requester", info.Requester)
	return info, nil
}

func (a *Approvals) Notify(info ApprovalInfo) {
	a.mu.Lock()
	p, ok := a.pending[info.ApprovalID]
	if ok {
		p.timer = time.AfterFunc(a.timeout, func() {
			a.finish(info.ApprovalID, ApprovalDecision{Approved: false, Reason: "审批超时"})
		})
	}
	a.mu.Unlock()
	if !ok {
		a.log.Warn("notify for unknown approval", "approvalId", info.ApprovalID)
		return
	}
	err := a.sender.SendProtocol(protocol.TypeApprovalRequest, protocol.ApprovalRequest{
		ApprovalID: info.ApprovalID,
		Tool:       info.Tool,
		Args:       info.Args,
		Requester:  info.Requester,
		Prompt:     info.Prompt,
		TimeoutMS:  a.timeout.Milliseconds(),
	})
	if err != nil {
		a.log.Warn("approval request send failed", "err", err)
		a.finish(info.ApprovalID, ApprovalDecision{Approved: false, Reason: "后端无法联系 Minecraft 服务器"})
	}
}

func (a *Approvals) HandleResult(res protocol.ApprovalResult) {
	a.finish(res.ApprovalID, ApprovalDecision{
		Approved: res.Approved,
		Operator: res.Operator,
		Reason:   res.Reason,
	})
}

func (a *Approvals) Pending() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pending)
}

func (a *Approvals) DrainDecided() []Outcome {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.decided) == 0 {
		return nil
	}
	out := make([]Outcome, 0, len(a.decided))
	for _, outcome := range a.decided {
		out = append(out, outcome)
	}
	a.decided = make(map[string]Outcome)
	return out
}

func (a *Approvals) finish(id string, decision ApprovalDecision) {
	a.mu.Lock()
	p, ok := a.pending[id]
	if ok {
		delete(a.pending, id)
		if p.timer != nil {
			p.timer.Stop()
		}
	}
	if ok {
		a.decided[id] = Outcome{ApprovalID: id, Info: p.info, Decision: decision}
	}
	a.mu.Unlock()
	if !ok {
		a.log.Warn("approval result for unknown or expired request", "approvalId", id)
		return
	}
	a.log.Info("approval decided",
		"approvalId", id,
		"tool", p.info.Tool,
		"approved", decision.Approved,
		"operator", decision.Operator,
		"reason", decision.Reason,
	)
	select {
	case a.notify <- struct{}{}:
	default:
	}
}
