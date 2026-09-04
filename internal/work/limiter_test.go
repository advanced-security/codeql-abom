package work

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock records what a limiter asked to sleep for, without any real waiting.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	slept  []time.Duration
	cancel error
}

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
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
	if c.cancel != nil {
		return c.cancel
	}
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
	return nil
}

func (c *fakeClock) waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.slept))
	copy(out, c.slept)
	return out
}

func paced(perHour int) (*Limiter, *fakeClock) {
	c := newClock()
	l := NewLimiter(perHour)
	l.now, l.sleep = c.Now, c.Sleep
	return l, c
}

func TestLimiterSpacesRequestsEvenly(t *testing.T) {
	l, c := paced(3600) // one per second
	for i := 0; i < 4; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("Wait: %v", err)
		}
	}

	waits := c.waits()
	if len(waits) != 3 {
		t.Fatalf("slept %d times, want 3: the first request should not wait", len(waits))
	}
	for i, w := range waits {
		if w != time.Second {
			t.Errorf("wait %d = %v, want 1s", i, w)
		}
	}
}

// Capping the rate is the point; adding workers must shorten queueing, not raise the rate.
func TestLimiterHoldsRateUnderConcurrency(t *testing.T) {
	l, c := paced(3600)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.Wait(context.Background()); err != nil {
				t.Errorf("Wait: %v", err)
			}
		}()
	}
	wg.Wait()

	// Eight requests at one per second occupy seven seconds of schedule regardless of how
	// many goroutines asked.
	var total time.Duration
	for _, w := range c.waits() {
		total += w
	}
	if total != 7*time.Second {
		t.Errorf("scheduled %v of waiting, want 7s", total)
	}
}

// Time already spent working counts against the interval, or a slow job would be charged twice.
func TestLimiterDoesNotWaitWhenTheIntervalHasPassed(t *testing.T) {
	l, c := paced(3600)
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	c.mu.Lock()
	c.now = c.now.Add(5 * time.Second)
	c.mu.Unlock()

	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(c.waits()) != 0 {
		t.Errorf("waited %v despite the interval having elapsed", c.waits())
	}
}

func TestLimiterZeroMeansUnlimited(t *testing.T) {
	l, c := paced(0)
	for i := 0; i < 5; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("Wait: %v", err)
		}
	}
	if len(c.waits()) != 0 {
		t.Errorf("an unlimited limiter waited: %v", c.waits())
	}
}

// A nil limiter is the "no pacing configured" case and must not need a guard at every call site.
func TestNilLimiterIsUsable(t *testing.T) {
	var l *Limiter
	if err := l.Wait(context.Background()); err != nil {
		t.Errorf("Wait on nil limiter: %v", err)
	}
}

func TestLimiterStopsOnCancellation(t *testing.T) {
	l, _ := paced(1) // one per hour, so the second request would wait a long time
	ctx, cancel := context.WithCancel(context.Background())

	if err := l.Wait(ctx); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	cancel()
	if err := l.Wait(ctx); err == nil {
		t.Error("Wait returned nil after cancellation")
	}
}

// The real sleep, not just the fake, has to abandon a wait when the run is interrupted.
func TestLimiterRealSleepStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(ctx, time.Hour); err == nil {
		t.Error("sleepContext returned nil for a canceled context")
	}
}

func TestLimiterIntervalFromRate(t *testing.T) {
	for _, tc := range []struct {
		perHour int
		want    time.Duration
	}{
		{3600, time.Second},
		{5000, 720 * time.Millisecond},
		{60, time.Minute},
	} {
		if got := NewLimiter(tc.perHour).interval; got != tc.want {
			t.Errorf("NewLimiter(%d).interval = %v, want %v", tc.perHour, got, tc.want)
		}
	}
}
