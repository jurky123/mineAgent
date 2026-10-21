package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// sessionPool keeps a single FIFO waiting list. With concurrency=1, enqueue
// order is execution order across all channels. Larger explicit concurrency
// still serializes each session, without one goroutine per idle session.
type sessionPool struct {
	mu                  sync.Mutex
	queues              map[string]int
	pending             []*poolWork
	running             map[string]bool
	active, concurrency int
}
type poolWork struct {
	key  string
	fn   func()
	stop func() bool
}

func newSessionPool(concurrency int, _ time.Duration, _ *slog.Logger) *sessionPool {
	if concurrency <= 0 {
		concurrency = 1
	}
	return &sessionPool{queues: make(map[string]int), running: make(map[string]bool), concurrency: concurrency}
}
func (p *sessionPool) enqueue(ctx context.Context, key string, fn func()) bool {
	return p.enqueueCancelable(ctx, key, fn, nil)
}
func (p *sessionPool) enqueueCancelable(ctx context.Context, key string, fn, onCanceled func()) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil || len(p.pending) >= 256 || p.queues[key] >= 17 {
		return false
	}
	p.queues[key]++
	work := &poolWork{key: key, fn: fn}
	p.pending = append(p.pending, work)
	work.stop = context.AfterFunc(ctx, func() {
		p.mu.Lock()
		found := false
		for i, w := range p.pending {
			if w == work {
				copy(p.pending[i:], p.pending[i+1:])
				p.pending[len(p.pending)-1] = nil
				p.pending = p.pending[:len(p.pending)-1]
				p.queues[key]--
				if p.queues[key] == 0 {
					delete(p.queues, key)
				}
				found = true
				break
			}
		}
		p.dispatchLocked()
		p.mu.Unlock()
		if found && onCanceled != nil {
			onCanceled()
		}
	})
	p.dispatchLocked()
	return true
}
func (p *sessionPool) dispatchLocked() {
	for p.active < p.concurrency {
		i := 0
		for i < len(p.pending) && p.running[p.pending[i].key] {
			i++
		}
		if i == len(p.pending) {
			return
		}
		work := p.pending[i]
		copy(p.pending[i:], p.pending[i+1:])
		p.pending[len(p.pending)-1] = nil
		p.pending = p.pending[:len(p.pending)-1]
		p.active++
		p.running[work.key] = true
		work.stop()
		go func() {
			defer func() {
				p.mu.Lock()
				defer p.mu.Unlock()
				p.active--
				delete(p.running, work.key)
				p.queues[work.key]--
				if p.queues[work.key] == 0 {
					delete(p.queues, work.key)
				}
				p.dispatchLocked()
			}()
			work.fn()
		}()
	}
}
