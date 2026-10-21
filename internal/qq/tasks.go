package qq

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"mineagent/internal/agent"
)

// Task control never waits for the LLM queue. Group users may only cancel their
// own tasks; configured QQ administrators may stop this group's tasks.
type qqTask struct {
	mu                    sync.Mutex
	ctx                   context.Context
	cancel                context.CancelFunc
	done                  chan struct{}
	owner, target, status string
	started               bool
	at                    time.Time
	once                  sync.Once
}

func (c *Channel) newTask(key, owner, target string) *qqTask {
	ctx, cancel := context.WithCancel(context.Background())
	task := &qqTask{ctx: ctx, cancel: cancel, done: make(chan struct{}), owner: owner, target: target, status: "已排队", at: time.Now()}
	c.mu.Lock()
	if c.tasks == nil {
		c.tasks = map[string][]*qqTask{}
	}
	c.tasks[key] = append(c.tasks[key], task)
	c.mu.Unlock()
	return task
}
func (c *Channel) finishTask(key string, t *qqTask) {
	t.once.Do(func() {
		close(t.done)
		c.mu.Lock()
		q := c.tasks[key]
		for i, x := range q {
			if x == t {
				q = append(q[:i], q[i+1:]...)
				break
			}
		}
		if len(q) == 0 {
			delete(c.tasks, key)
		} else {
			c.tasks[key] = q
		}
		c.mu.Unlock()
		// The progress worker closes any stream before cancelling its own lifetime.
	})
}
func (t *qqTask) start() {
	t.mu.Lock()
	t.started = true
	t.status = "正在处理"
	t.mu.Unlock()
}
func (t *qqTask) event(e agent.ToolEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch e.Status {
	case "started":
		t.status = "正在调用 " + e.Name
	case "approval":
		t.status = "等待管理员审批"
	case "failed":
		t.status = "工具返回错误，正在核查"
	case "returned":
		t.status = "工具已返回，继续处理"
	}
}
func (c *Channel) taskCommand(key, owner string, admin bool, command string) string {
	c.mu.Lock()
	tasks := append([]*qqTask(nil), c.tasks[key]...)
	c.mu.Unlock()
	n := 0
	var rows []string
	for _, t := range tasks {
		if t.owner != owner && !admin {
			continue
		}
		if command == "stop" {
			t.cancel()
			c.finishTask(key, t)
			n++
			continue
		}
		t.mu.Lock()
		status := t.status
		t.mu.Unlock()
		rows = append(rows, fmt.Sprintf("%d. %s · 已等待/运行 %s", len(rows)+1, status, time.Since(t.at).Round(time.Second)))
	}
	if command == "stop" {
		if c.ag != nil {
			c.ag.CancelPending(key)
		}
		if n == 0 {
			return "当前没有可停止的任务。"
		}
		return fmt.Sprintf("已请求停止 %d 个任务（包括排队任务）；已完成的操作不会撤销。", n)
	}
	if len(rows) == 0 {
		return "当前没有你的待处理任务。"
	}
	running, waiting := 0, 0
	if c.ag != nil {
		running, waiting = c.ag.QueueStatus()
	}
	return strings.Join(rows, "\n") + fmt.Sprintf("\n后端正在运行 %d 项，等待 %d 项。\n/stop 停止你的任务；/tasks 查看状态。", running, waiting)
}

// Updates contain tool lifecycle labels only, never hidden reasoning, arguments,
// raw tool results or another user's prompt. Calls run outside the agent worker.
func (c *Channel) taskProgress(t *qqTask) {
	defer t.cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	scene, id, msgID := splitTarget(t.target)
	var streamID, content, last string
	index := 0
	seq := 0
	disabled := false
	lastSend := time.Now()
	noticesFailed := false
	flush := func(done bool) {
		t.mu.Lock()
		status, started := t.status, t.started
		t.mu.Unlock()
		if t.ctx.Err() != nil {
			status = "已请求停止"
		}
		if done {
			status = "本次处理结束；结果或审批说明见回复。"
		}
		if !started && !done {
			status = "任务已排队，等待前面的请求完成。"
		}
		if scene == "c2c" && c.cfg.StreamProgress && !disabled && msgID != "" {
			if status == last && !done {
				return
			}
			if len([]rune(content)) > 1600 && !done {
				return
			}
			next := content
			if next != "" {
				next += "\n"
			}
			next += "· " + status
			if seq == 0 {
				seq = c.api.nextSequence("/v2/users/"+id+"/messages", msgID)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out, err := c.api.sendStream(ctx, id, msgID, streamID, next, index, seq, done)
			if err != nil {
				disabled = true
				c.log.Warn("qq progress stream unavailable")
				return
			}
			streamID = out
			content = next
			index++
			last = status
			return
		}
		// Groups cannot edit/stream a message. Sparse notices avoid flooding a group
		// and leave passive reply capacity for results and attachments.
		if !done && !noticesFailed && time.Since(lastSend) >= 45*time.Second && status != last {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if c.sendText(ctx, t.target, "任务进度："+status+"。可用 /tasks 查询，/stop 停止。") != nil {
				noticesFailed = true
			}
			lastSend = time.Now()
			last = status
		}
	}
	for {
		select {
		case <-t.done:
			if streamID != "" {
				flush(true)
			}
			return
		case <-t.ctx.Done():
			if streamID != "" {
				flush(true)
			}
			return
		case <-ticker.C:
			flush(false)
		}
	}
}
func (a *API) sendStream(ctx context.Context, id, msgID, streamID, text string, index, seq int, done bool) (string, error) {
	state := 1
	if done {
		state = 10
	}
	var out struct {
		ID string `json:"id"`
	}
	err := a.doJSON(ctx, "POST", "/v2/users/"+id+"/stream_messages", map[string]any{
		"input_mode": "replace", "input_state": state, "index": index, "content_type": "text", "content_raw": text, "msg_id": msgID, "stream_msg_id": streamID, "msg_seq": seq,
	}, &out)
	if err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("QQ 未确认流式消息 ID")
	}
	return out.ID, nil
}
