package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock lets the limiter be tested deterministically without real sleeping.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	slept []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1700000000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
	return nil
}

func (c *fakeClock) sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.slept))
	copy(out, c.slept)
	return out
}

func newTestLimiter(interval time.Duration) (*Limiter, *fakeClock) {
	clock := newFakeClock()
	l := New(interval)
	l.now = clock.Now
	l.sleep = clock.Sleep
	return l, clock
}

func TestFirstCallDoesNotWait(t *testing.T) {
	l, clock := newTestLimiter(time.Second)

	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	if got := clock.sleeps(); len(got) != 0 {
		t.Errorf("first call slept %v, want no sleep", got)
	}
}

func TestSuccessiveCallsAreSpacedByInterval(t *testing.T) {
	l, clock := newTestLimiter(time.Second)
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
	}

	got := clock.sleeps()
	if len(got) != 3 {
		t.Fatalf("got %d sleeps (%v), want 3", len(got), got)
	}
	for i, d := range got {
		if d != time.Second {
			t.Errorf("sleep %d = %v, want 1s", i, d)
		}
	}
}

func TestNoWaitWhenIntervalAlreadyElapsed(t *testing.T) {
	l, clock := newTestLimiter(time.Second)
	ctx := context.Background()

	if err := l.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	// Simulate time passing between requests (e.g. slow upstream response).
	clock.mu.Lock()
	clock.now = clock.now.Add(5 * time.Second)
	clock.mu.Unlock()

	if err := l.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	if got := clock.sleeps(); len(got) != 0 {
		t.Errorf("slept %v, want no sleep after interval elapsed", got)
	}
}

func TestZeroIntervalDisablesLimiting(t *testing.T) {
	l, clock := newTestLimiter(0)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
	}

	if got := clock.sleeps(); len(got) != 0 {
		t.Errorf("slept %v, want no sleep when disabled", got)
	}
}

func TestWaitRespectsCancelledContext(t *testing.T) {
	l, _ := newTestLimiter(time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := l.Wait(ctx); err == nil {
		t.Fatal("Wait() with cancelled context returned nil, want error")
	}
}

func TestWaitUnblocksWhenContextIsCancelledDuringSleep(t *testing.T) {
	// Uses the real clock to verify the select in sleepContext.
	l := New(time.Hour)

	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := l.Wait(ctx)
	if err == nil {
		t.Fatal("Wait() returned nil, want context deadline error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Wait() blocked for %v, want prompt cancellation", elapsed)
	}
}

func TestConcurrentCallersAreSerialized(t *testing.T) {
	const callers = 5
	l, clock := newTestLimiter(time.Second)

	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			if err := l.Wait(context.Background()); err != nil {
				t.Errorf("Wait() error = %v", err)
			}
		}()
	}
	wg.Wait()

	// One caller proceeds immediately, the rest each reserve a distinct slot.
	if got := len(clock.sleeps()); got != callers-1 {
		t.Errorf("got %d sleeps, want %d", got, callers-1)
	}
}
