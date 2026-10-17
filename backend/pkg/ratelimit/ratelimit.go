package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter enforces a minimum interval between successive operations across all
// goroutines. It is used to honour the Nominatim usage policy, which permits at
// most one request per second for the public endpoint.
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time

	// now and sleep are injectable for deterministic tests.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// New creates a Limiter allowing one operation per interval.
// A non-positive interval disables rate limiting.
func New(interval time.Duration) *Limiter {
	return &Limiter{
		interval: interval,
		now:      time.Now,
		sleep:    sleepContext,
	}
}

// Wait blocks until the caller may proceed or ctx is done.
//
// The reservation is made while holding the lock, so concurrent callers are
// spaced out by interval instead of all waking up at the same time.
func (l *Limiter) Wait(ctx context.Context) error {
	if l == nil || l.interval <= 0 {
		return ctx.Err()
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	l.mu.Lock()
	now := l.now()
	var wait time.Duration
	if l.last.IsZero() {
		// First call may proceed immediately.
		l.last = now
	} else {
		next := l.last.Add(l.interval)
		if next.After(now) {
			wait = next.Sub(now)
			l.last = next
		} else {
			l.last = now
		}
	}
	l.mu.Unlock()

	if wait <= 0 {
		return nil
	}

	return l.sleep(ctx, wait)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
