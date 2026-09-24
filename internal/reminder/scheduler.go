// Package reminder 到点把提醒发回原会话。
// 调度器每 20 秒扫一次库（单机轻量实现，不引消息队列）；
// 发送走各通道的 Deliver（会顺带注册通道，服务重启后也能送达）。
package reminder

import (
	"context"
	"log/slog"
	"time"

	"mineagent/internal/storage"
)

// Deliver 按通道把文本发到指定会话（由 main 注入各通道的实现）。
type Deliver func(ctx context.Context, sessionID, target, text string) error

type Scheduler struct {
	store   *storage.Store
	log     *slog.Logger
	deliver map[string]Deliver
	every   time.Duration
}

func New(store *storage.Store, log *slog.Logger, deliver map[string]Deliver) *Scheduler {
	return &Scheduler{store: store, log: log, deliver: deliver, every: 20 * time.Second}
}

func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.every)
	defer ticker.Stop()
	s.log.Info("reminder scheduler started", "every", s.every.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	due, err := s.store.DueReminders(ctx, time.Now().UnixMilli(), 20)
	if err != nil {
		s.log.Warn("reminder scan failed", "err", err)
		return
	}
	for _, r := range due {
		// 先标记完成再发送：避免发送慢导致下一轮重复发
		if err := s.store.CompleteReminder(ctx, r.ID); err != nil {
			s.log.Warn("reminder complete failed", "id", r.ID, "err", err)
			continue
		}
		s.fire(ctx, r)
	}
}

func (s *Scheduler) fire(ctx context.Context, r storage.Reminder) {
	d := s.deliver[r.Channel]
	if d == nil {
		s.log.Warn("reminder channel not deliverable", "id", r.ID, "channel", r.Channel)
		return
	}
	fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	text := "⏰ 提醒：" + r.Text
	if err := d(fctx, r.SessionID, r.Target, text); err != nil {
		s.log.Warn("reminder deliver failed", "id", r.ID, "channel", r.Channel, "err", err)
		return
	}
	s.log.Info("reminder delivered", "id", r.ID, "channel", r.Channel, "session", r.SessionID)
}
