package body

import (
	"context"
	"time"

	"mineagent/internal/protocol"
)

// RunContext watches authority even while the model is thinking or waiting for
// rate limits. A stop must end the whole task, not just its current game action.
func (p *Principal) RunContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	if p.service == nil {
		return ctx, func() { cancel(nil) }
	}
	s := p.service
	changed := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[changed] = struct{}{}
	s.mu.Unlock()
	go func() {
		defer func() { s.mu.Lock(); delete(s.subs, changed); s.mu.Unlock() }()
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			if err := s.Check(ctx, p, false); err != nil {
				cancel(err)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-changed:
			case <-tick.C:
			}
		}
	}()
	return ctx, func() { cancel(nil) }
}

// health is backend knowledge, distinct from a successful game observation.
// In particular a local preflight error never implies that the WS is offline.
func (s *Service) health(p *Principal) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := map[string]any{
		"source": "backend", "connectionId": s.connection, "controlEpoch": s.epoch,
		"controlConnected": s.peer != nil,
		"ready":            s.ready, "gameConnected": s.peer != nil && s.state.Connected,
		"paused": s.paused || s.state.Paused, "draining": s.draining,
		"busy": s.busy, "stateStale": s.peer == nil || s.received.IsZero() || time.Since(s.received) > 10*time.Second,
		"taskLimits": map[string]int{"maxSteps": s.cfg.MaxSteps, "runTimeoutSec": s.cfg.RunTimeoutSec},
	}
	if p != nil {
		p.mu.Lock()
		d["runEpoch"] = p.Epoch
		p.mu.Unlock()
	}
	return d
}

func (s *Service) toolFailure(id, code string, p *Principal) protocol.BodyResult {
	r := failure(id, code, "not_executed")
	if code == "not_operator" {
		return r
	}
	d := s.health(p)
	d["phase"] = "backend_preflight"
	r.ConnectionID, r.ControlEpoch = d["connectionId"].(string), d["controlEpoch"].(uint64)
	switch code {
	case "stale":
		r.Summary = "动作快照已变化或过期，尚未执行；不代表身体通道断开。先用 body_status 和真实观察核查，不能盲目重试。"
		if d["runEpoch"] != r.ControlEpoch {
			r.Summary = "当前任务绑定的控制代次已失效；不代表身体通道断开。先用 body_status 核查，旧任务不能继续写入，请重新提交任务。"
		}
	case "body_offline":
		r.Summary = "身体控制端或游戏连接尚未就绪；用 body_status 区分连接、握手和状态过期。"
	case "body_paused":
		r.Summary = "身体已暂停；只读查询仍可用，行动需要管理员在面板恢复。"
	case "spawn_protected":
		r.Summary = "目标方块位于出生点四周 512 格保护区内，禁止挖掘或放置；移动、观察和背包不受此限制，请到区外施工。"
	case "protection_unavailable":
		r.Summary = "服务器尚未确认方块保护状态，暂不执行挖掘或放置；移动和观察仍可用。需要新版 Paper 插件提供保护状态，不是缺少活动区域授权。"
	}
	r.Data = []byte(output(d))
	return r
}
