package body

import (
	"context"
	"errors"
	"time"
)

type rateLimited struct{ wait time.Duration }

func (rateLimited) Error() string { return "rate_limited" }

// Check every bucket before reserving any token. A rejected reservation must
// not consume the broader action quota and starve a long-running task.
func (s *Service) reserveLocked(budgets map[string]int) error {
	now := time.Now()
	var wait time.Duration
	for key, n := range budgets {
		old := s.limits[key]
		i := 0
		for i < len(old) && now.Sub(old[i]) >= time.Minute {
			i++
		}
		old = old[i:]
		s.limits[key] = old
		if len(old) >= n {
			if delay := time.Minute - now.Sub(old[0]); delay > wait {
				wait = delay
			}
		}
	}
	if wait > 0 {
		return rateLimited{wait}
	}
	for key := range budgets {
		s.limits[key] = append(s.limits[key], now)
	}
	return nil
}

func (s *Service) waitAcquire(ctx context.Context, tool string, p *Principal) error {
	for {
		if err := s.check(ctx, p, false, isRead(tool)); err != nil {
			return err
		}
		err := s.acquire(tool, p)
		var limited rateLimited
		if !errors.As(err, &limited) {
			return err
		}
		// Brief checks keep stop, drain and revoked authority responsive even
		// outside a model run. No action has been dispatched while waiting.
		wait := min(limited.wait+250*time.Millisecond, 500*time.Millisecond)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
