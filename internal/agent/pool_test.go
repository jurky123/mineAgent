package agent

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestPoolParallelAcrossSessions(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := newSessionPool(2, time.Minute, log)
	ctx := context.Background()

	var mu sync.Mutex
	running, maxRunning := 0, 0
	a1Done := false
	release := make(chan struct{})
	done := make(chan string, 4)

	task := func(name string) func() {
		return func() {
			mu.Lock()
			running++
			if running > maxRunning {
				maxRunning = running
			}
			if name == "a2" && !a1Done {
				t.Errorf("同一会话必须串行：a2 在 a1 结束前就跑起来了")
			}
			mu.Unlock()
			<-release
			mu.Lock()
			running--
			if name == "a1" {
				a1Done = true
			}
			mu.Unlock()
			done <- name
		}
	}

	if !p.enqueue(ctx, "a", task("a1")) || !p.enqueue(ctx, "b", task("b1")) {
		t.Fatal("enqueue 失败")
	}
	if !p.enqueue(ctx, "a", task("a2")) {
		t.Fatal("enqueue a2 失败")
	}
	// 让两个会话各占一个并发位，等它们真正跑起来。
	time.Sleep(50 * time.Millisecond)
	close(release)

	got := map[string]bool{}
	for i := 0; i < 3; i++ {
		select {
		case name := <-done:
			got[name] = true
		case <-time.After(3 * time.Second):
			t.Fatalf("任务没跑完: %v", got)
		}
	}
	mu.Lock()
	m := maxRunning
	mu.Unlock()
	if m != 2 {
		t.Fatalf("并发数 = %d, want 2", m)
	}
	for _, name := range []string{"a1", "a2", "b1"} {
		if !got[name] {
			t.Fatalf("缺任务 %s", name)
		}
	}
}

func TestPoolIdleExitAndReuse(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := newSessionPool(1, 40*time.Millisecond, log)
	ctx := context.Background()

	ran := make(chan struct{}, 2)
	if !p.enqueue(ctx, "s", func() { ran <- struct{}{} }) {
		t.Fatal("enqueue 失败")
	}
	<-ran

	// 等 idle 退出
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		n := len(p.queues)
		p.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.mu.Lock()
	n := len(p.queues)
	p.mu.Unlock()
	if n != 0 {
		t.Fatalf("空闲 worker 没退出: %d", n)
	}

	// 再投一个：应自动建新 worker，任务不丢
	if !p.enqueue(ctx, "s", func() { ran <- struct{}{} }) {
		t.Fatal("enqueue 2 失败")
	}
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("空闲退出后任务丢了")
	}
}

func TestPoolQueueFull(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := newSessionPool(1, time.Minute, log)
	ctx := context.Background()
	release := make(chan struct{})
	if !p.enqueue(ctx, "s", func() { <-release }) {
		t.Fatal("enqueue 1 失败")
	}
	time.Sleep(20 * time.Millisecond)
	n := 0
	for i := 0; i < 64; i++ {
		if p.enqueue(ctx, "s", func() {}) {
			n++
		} else {
			break
		}
	}
	if n == 0 {
		t.Fatal("队列一个都塞不进")
	}
	close(release)
}
