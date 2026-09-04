package work

import (
	"context"
	"sync"
	"time"
)

// Limiter paces outbound requests across workers.
//
// A run spanning tens of thousands of repositories makes far more requests than any single
// session, so throughput has to be capped independently of worker count: raising concurrency
// should shorten queueing, not increase the rate a host sees.
//
// Requests are spaced evenly rather than issued in bursts, because a steady rate is what
// remains within a host's tolerance over hours.
type Limiter struct {
	interval time.Duration
	// sleep is overridable in tests.
	sleep func(context.Context, time.Duration) error
	now   func() time.Time

	mu sync.Mutex
	// next is the earliest time the following request may be issued.
	next time.Time
}

// NewLimiter caps requests to perHour. Zero or less means unlimited.
func NewLimiter(perHour int) *Limiter {
	l := &Limiter{now: time.Now, sleep: sleepContext}
	if perHour > 0 {
		l.interval = time.Hour / time.Duration(perHour)
	}
	return l
}

// Wait blocks until the caller may issue its request.
func (l *Limiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l == nil || l.interval <= 0 {
		return nil
	}

	l.mu.Lock()
	now := l.now()
	if l.next.Before(now) {
		l.next = now
	}
	wait := l.next.Sub(now)
	l.next = l.next.Add(l.interval)
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
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
