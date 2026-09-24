package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// sessionPool 是"按会话串行、跨会话并行"的任务池：
// 每个 sessionKey 一个队列 + 一个 worker（同一会话的消息严格按序处理），
// 不同会话各跑各的（QQ 跑长任务时不会把网页/MC 的请求堵在后面）。
// 全局并发用信号量限制（默认 4，见 config.Agent.MaxConcurrentRuns）。
// worker 空闲 idle 后退出并从表里摘掉，避免会话多了 goroutine 无限堆积；
// 退出前会再确认队列为空，保证不丢任务。
type sessionPool struct {
	mu     sync.Mutex
	queues map[string]chan func()
	sem    chan struct{}
	idle   time.Duration
	log    *slog.Logger
}

func newSessionPool(concurrency int, idle time.Duration, log *slog.Logger) *sessionPool {
	if concurrency <= 0 {
		concurrency = 4
	}
	if idle <= 0 {
		idle = 10 * time.Minute
	}
	return &sessionPool{
		queues: make(map[string]chan func()),
		sem:    make(chan struct{}, concurrency),
		idle:   idle,
		log:    log,
	}
}

// enqueue 投递任务。队列满/ctx 结束返回 false（调用方按"忙不过来"处理）。
func (p *sessionPool) enqueue(ctx context.Context, key string, fn func()) bool {
	p.mu.Lock()
	q := p.queues[key]
	if q == nil {
		q = make(chan func(), 16)
		p.queues[key] = q
		go p.worker(ctx, key, q)
	}
	p.mu.Unlock()
	select {
	case q <- fn:
		return true
	case <-ctx.Done():
		return false
	default:
		return false
	}
}

func (p *sessionPool) worker(ctx context.Context, key string, q chan func()) {
	timer := time.NewTimer(p.idle)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case fn := <-q:
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(p.idle)
			select {
			case p.sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			fn()
			<-p.sem
		case <-timer.C:
			// 空闲退出：先确认真的没活儿了（enqueue 可能刚塞进来）。
			p.mu.Lock()
			if len(q) > 0 {
				p.mu.Unlock()
				timer.Reset(p.idle)
				continue
			}
			if p.queues[key] == q {
				delete(p.queues, key)
			}
			p.mu.Unlock()
			return
		}
	}
}
