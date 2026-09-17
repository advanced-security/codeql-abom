package work

import (
	"context"
	"testing"
	"time"
)

// Pacing that cannot be seen cannot be tuned. A crawl that spent half its time waiting and one
// that never waited look identical from the outside, and only one of them is evidence the rate is
// set wrong.
func TestWaitingIsCountedAndTimed(t *testing.T) {
	l := NewLimiter(3600) // one per second
	var slept time.Duration
	now := time.Unix(0, 0)
	l.now = func() time.Time { return now }
	l.sleep = func(_ context.Context, d time.Duration) error {
		slept += d
		now = now.Add(d)
		return nil
	}

	for i := 0; i < 3; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	waits, total := l.Waited()
	// The first caller is not held; the two after it are.
	if waits != 2 {
		t.Errorf("two callers were held; got %d", waits)
	}
	if total != slept {
		t.Errorf("the time reported must be the time spent; got %s against %s", total, slept)
	}
}

func TestALimiterNobodyWaitedOnReportsNothing(t *testing.T) {
	l := NewLimiter(0)
	if err := l.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if waits, total := l.Waited(); waits != 0 || total != 0 {
		t.Errorf("an unlimited limiter holds nobody; got %d waits over %s", waits, total)
	}
}

// The rate has to move while the crawl runs, because the budget it should follow is only known
// after the first answer comes back.
func TestTheRateCanBeChangedWhileRunning(t *testing.T) {
	l := NewLimiter(0)
	if got := l.Interval(); got != 0 {
		t.Fatalf("unlimited to begin with; got %s", got)
	}

	l.SetPerHour(3600)
	if got := l.Interval(); got != time.Second {
		t.Errorf("3600 an hour is one a second; got %s", got)
	}

	// Back to unlimited, because a budget that recovered must not leave the crawl throttled.
	l.SetPerHour(0)
	if got := l.Interval(); got != 0 {
		t.Errorf("zero is unlimited; got %s", got)
	}
}

func TestANilLimiterStillReportsNothing(t *testing.T) {
	var l *Limiter
	if waits, total := l.Waited(); waits != 0 || total != 0 {
		t.Errorf("a limiter that does not exist held nobody; got %d over %s", waits, total)
	}
	l.SetPerHour(10) // must not panic
}
