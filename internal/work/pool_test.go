package work

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func jobs(n int) []Job {
	out := make([]Job, n)
	for i := range out {
		out[i] = Job{Repository: fmt.Sprintf("octo-org/repo-%d", i)}
	}
	return out
}

func TestRunProcessesEveryJobExactlyOnce(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}

	p := &Pool{
		Concurrency: 4,
		Prepare: func(_ context.Context, j Job) (Prepared, error) {
			return Prepared{Job: j}, nil
		},
		Consume: func(_ context.Context, pr Prepared) (int, error) {
			mu.Lock()
			defer mu.Unlock()
			seen[pr.Job.Repository]++
			return 1, nil
		},
	}

	stats, err := p.Run(context.Background(), jobs(50))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Processed != 50 {
		t.Errorf("Processed = %d, want 50", stats.Processed)
	}
	if stats.Rows != 50 {
		t.Errorf("Rows = %d, want 50", stats.Rows)
	}
	if len(seen) != 50 {
		t.Fatalf("consumed %d distinct jobs, want 50", len(seen))
	}
	for repo, n := range seen {
		if n != 1 {
			t.Errorf("%s consumed %d times, want 1", repo, n)
		}
	}
}

// The store is a single writer, so Consume must never run concurrently. Everything expensive
// belongs in Prepare instead.
func TestConsumeIsNeverConcurrent(t *testing.T) {
	var inFlight, maxInFlight int32

	p := &Pool{
		Concurrency: 8,
		Prepare: func(_ context.Context, j Job) (Prepared, error) {
			return Prepared{Job: j}, nil
		},
		Consume: func(_ context.Context, pr Prepared) (int, error) {
			n := atomic.AddInt32(&inFlight, 1)
			for {
				old := atomic.LoadInt32(&maxInFlight)
				if n <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			return 0, nil
		},
	}

	if _, err := p.Run(context.Background(), jobs(40)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&maxInFlight); got != 1 {
		t.Errorf("Consume ran with %d concurrent callers, want 1", got)
	}
}

// 20,000 repositories must never be processed as one batch.
func TestPrepareConcurrencyIsBounded(t *testing.T) {
	const limit = 4
	var inFlight, maxInFlight int32

	p := &Pool{
		Concurrency: limit,
		Prepare: func(_ context.Context, j Job) (Prepared, error) {
			n := atomic.AddInt32(&inFlight, 1)
			for {
				old := atomic.LoadInt32(&maxInFlight)
				if n <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			return Prepared{Job: j}, nil
		},
		Consume: func(context.Context, Prepared) (int, error) { return 0, nil },
	}

	if _, err := p.Run(context.Background(), jobs(60)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&maxInFlight); got > limit {
		t.Errorf("peak concurrency %d exceeded limit %d", got, limit)
	}
	if got := atomic.LoadInt32(&maxInFlight); got < 2 {
		t.Errorf("peak concurrency %d suggests work never ran in parallel", got)
	}
}

// One bad repository must not abandon the other 19,999.
func TestFailuresDoNotAbortTheRun(t *testing.T) {
	var failures []string
	var mu sync.Mutex

	p := &Pool{
		Concurrency: 4,
		Prepare: func(_ context.Context, j Job) (Prepared, error) {
			if j.Repository == "octo-org/repo-3" {
				return Prepared{}, errors.New("boom")
			}
			return Prepared{Job: j}, nil
		},
		Consume: func(context.Context, Prepared) (int, error) { return 1, nil },
		OnEvent: func(e Event) {
			if e.Kind == EventFailed {
				mu.Lock()
				failures = append(failures, e.Job.Repository)
				mu.Unlock()
			}
		},
	}

	stats, err := p.Run(context.Background(), jobs(10))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1", stats.Failed)
	}
	if stats.Processed != 9 {
		t.Errorf("Processed = %d, want 9", stats.Processed)
	}
	if len(failures) != 1 || failures[0] != "octo-org/repo-3" {
		t.Errorf("failures = %v", failures)
	}
}

// Repositories with no actions database, or already recorded as done, are an expected outcome
// and must be counted rather than treated as errors.
func TestSkippedJobsAreCountedNotFailed(t *testing.T) {
	p := &Pool{
		Concurrency: 4,
		Prepare: func(_ context.Context, j Job) (Prepared, error) {
			if j.Repository == "octo-org/repo-1" {
				return Prepared{}, fmt.Errorf("no database: %w", ErrSkip)
			}
			return Prepared{Job: j}, nil
		},
		Consume: func(context.Context, Prepared) (int, error) { return 1, nil },
	}

	stats, err := p.Run(context.Background(), jobs(5))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", stats.Skipped)
	}
	if stats.Failed != 0 {
		t.Errorf("Failed = %d, want 0", stats.Failed)
	}
	if stats.Processed != 4 {
		t.Errorf("Processed = %d, want 4", stats.Processed)
	}
}

func TestConsumeFailureIsRecorded(t *testing.T) {
	p := &Pool{
		Concurrency: 2,
		Prepare: func(_ context.Context, j Job) (Prepared, error) {
			return Prepared{Job: j}, nil
		},
		Consume: func(_ context.Context, pr Prepared) (int, error) {
			if pr.Job.Repository == "octo-org/repo-0" {
				return 0, errors.New("write failed")
			}
			return 1, nil
		},
	}

	stats, err := p.Run(context.Background(), jobs(3))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Failed != 1 || stats.Processed != 2 {
		t.Errorf("Failed = %d Processed = %d, want 1 and 2", stats.Failed, stats.Processed)
	}
}

func TestCancellationStopsTheRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var consumed int32

	p := &Pool{
		Concurrency: 2,
		Prepare: func(c context.Context, j Job) (Prepared, error) {
			return Prepared{Job: j}, c.Err()
		},
		Consume: func(_ context.Context, pr Prepared) (int, error) {
			if atomic.AddInt32(&consumed, 1) == 5 {
				cancel()
			}
			return 1, nil
		},
	}

	_, err := p.Run(ctx, jobs(1000))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if n := atomic.LoadInt32(&consumed); n > 200 {
		t.Errorf("consumed %d jobs after cancellation, expected an early stop", n)
	}
}

func TestZeroConcurrencyDefaultsToSerial(t *testing.T) {
	p := &Pool{
		Prepare: func(_ context.Context, j Job) (Prepared, error) { return Prepared{Job: j}, nil },
		Consume: func(context.Context, Prepared) (int, error) { return 1, nil },
	}

	stats, err := p.Run(context.Background(), jobs(3))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Processed != 3 {
		t.Errorf("Processed = %d, want 3", stats.Processed)
	}
}

func TestEmptyJobListIsNotAnError(t *testing.T) {
	p := &Pool{
		Prepare: func(_ context.Context, j Job) (Prepared, error) { return Prepared{Job: j}, nil },
		Consume: func(context.Context, Prepared) (int, error) { return 0, nil },
	}

	stats, err := p.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Processed != 0 {
		t.Errorf("Processed = %d, want 0", stats.Processed)
	}
}

func TestMissingPrepareOrConsumeIsRejected(t *testing.T) {
	if _, err := (&Pool{Consume: func(context.Context, Prepared) (int, error) {
		return 0, nil
	}}).Run(context.Background(), jobs(1)); err == nil {
		t.Error("expected an error when Prepare is nil")
	}
	if _, err := (&Pool{Prepare: func(context.Context, Job) (Prepared, error) {
		return Prepared{}, nil
	}}).Run(context.Background(), jobs(1)); err == nil {
		t.Error("expected an error when Consume is nil")
	}
}
