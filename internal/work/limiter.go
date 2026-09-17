package work

import (
	"context"
	"sync"
	"sync/atomic"
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
	// sleep is overridable in tests.
	sleep func(context.Context, time.Duration) error
	now   func() time.Time

	waits  atomic.Int64
	waited atomic.Int64

	mu sync.Mutex
	// interval is guarded because the rate follows a budget that is only known once an answer
	// has come back.
	interval time.Duration
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
	if l == nil {
		return nil
	}

	l.mu.Lock()
	if l.interval <= 0 {
		l.mu.Unlock()
		return nil
	}
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
	// Counted before the sleep: the caller was held whether or not the wait ran to completion.
	l.waits.Add(1)
	l.waited.Add(int64(wait))
	return l.sleep(ctx, wait)
}

// SetPerHour changes the pace while the crawl runs. Zero or less means unlimited.
//
// Mutable because the budget worth following is only reported by the answers themselves, and a
// budget that recovered must be able to lift a throttle it caused.
func (l *Limiter) SetPerHour(perHour int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if perHour > 0 {
		l.interval = time.Hour / time.Duration(perHour)
		return
	}
	l.interval = 0
}

// Interval reports the current spacing between requests. Zero is unlimited.
func (l *Limiter) Interval() time.Duration {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.interval
}

// Waited reports how many callers were held, and for how long in total.
//
// A crawl that waited and one that did not are different evidence about whether the rate is set
// correctly, and absorbing the difference silently loses the only signal there is.
func (l *Limiter) Waited() (int, time.Duration) {
	if l == nil {
		return 0, 0
	}
	return int(l.waits.Load()), time.Duration(l.waited.Load())
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
