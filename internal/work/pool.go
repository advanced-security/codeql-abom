// Package work runs ABOM's post-processing across many repositories.
//
// mrva-system and mrvahepc cover listing databases, fetching them, running queries and
// returning results. What remains is ABOM's own work: decoding BQRS and SARIF, classifying
// references, and aggregating them into the graph. That still needs a pool, because it has to
// run over tens of thousands of repositories without processing them as one batch.
package work

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrSkip marks a job that had nothing to do, such as a repository with no CodeQL database or
// one the ingest ledger has already recorded as complete. Skips are an expected outcome at
// organization scale and are counted separately from failures.
var ErrSkip = errors.New("skip")

// Job identifies one repository to process.
type Job struct {
	Repository string
}

// Prepared is the output of the parallel phase, ready for serialized persistence.
type Prepared struct {
	Job Job
	// ResultPath is the decoded query output awaiting ingestion.
	ResultPath string
	// ResultQueryID identifies the query that produced ResultPath. A suite decides which query
	// that is, so it cannot come from a flag.
	ResultQueryID string
	// Extra carries any further decoded results the parallel phase produced, keyed by the query
	// that produced each. A suite grows over time, so the number of results per repository is
	// not fixed and cannot be a field per query.
	Extra map[string]string
	// SarifPath is the findings lane's output. It is a field of its own rather than an Extra
	// entry because SARIF is read by a different ingester than everything in Extra.
	SarifPath string
	// Commit is the database revision the result came from, for ledger bookkeeping.
	Commit string
	// AlreadyRecorded means the ledger has this exact work on record and nothing was run. It
	// travels as a result rather than as ErrSkip because the job still has to be settled, and
	// only Consume may write.
	AlreadyRecorded bool
}

// Event kinds reported through Pool.OnEvent.
const (
	EventDone    = "done"
	EventSkipped = "skipped"
	EventFailed  = "failed"
)

// Event reports the outcome of one job.
type Event struct {
	Kind string
	Job  Job
	Rows int
	Err  error
}

// Stats summarizes a run.
type Stats struct {
	Processed int
	Skipped   int
	Failed    int
	Rows      int
}

// Pool runs Prepare concurrently and Consume serially.
//
// The split is deliberate: the expensive work (network, CodeQL evaluation, decoding)
// parallelises freely, while persistence goes through a single goroutine because the SQLite
// store admits exactly one writer.
type Pool struct {
	// Concurrency bounds simultaneous Prepare calls. Zero means serial.
	Concurrency int

	// Prepare does the parallelizable work. Returning an error wrapping ErrSkip records a
	// skip rather than a failure.
	Prepare func(context.Context, Job) (Prepared, error)

	// Consume persists one prepared result and reports how many rows it contributed. It is
	// never called concurrently.
	Consume func(context.Context, Prepared) (int, error)

	// OnEvent, when set, is called from the serialized phase, so implementations need no
	// locking of their own.
	OnEvent func(Event)
}

// Run processes every job, returning aggregate statistics.
//
// A failing job is recorded and the run continues; only cancellation stops the pool, so one
// unreachable repository cannot abandon the rest of the organization.
func (p *Pool) Run(ctx context.Context, jobs []Job) (Stats, error) {
	if p.Prepare == nil {
		return Stats{}, errors.New("work: Pool.Prepare is required")
	}
	if p.Consume == nil {
		return Stats{}, errors.New("work: Pool.Consume is required")
	}
	if len(jobs) == 0 {
		return Stats{}, nil
	}

	concurrency := p.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type outcome struct {
		prepared Prepared
		job      Job
		err      error
	}

	jobCh := make(chan Job)
	outCh := make(chan outcome, concurrency)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				prepared, err := p.Prepare(ctx, job)
				select {
				case outCh <- outcome{prepared: prepared, job: job, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobCh)
		for _, job := range jobs {
			select {
			case jobCh <- job:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(outCh)
	}()

	var stats Stats
	for out := range outCh {
		switch {
		case out.err != nil && errors.Is(out.err, ErrSkip):
			stats.Skipped++
			p.emit(Event{Kind: EventSkipped, Job: out.job, Err: out.err})

		case out.err != nil:
			stats.Failed++
			p.emit(Event{Kind: EventFailed, Job: out.job, Err: out.err})

		default:
			rows, err := p.Consume(ctx, out.prepared)
			if err != nil {
				stats.Failed++
				p.emit(Event{Kind: EventFailed, Job: out.job, Err: err})
				break
			}
			stats.Processed++
			stats.Rows += rows
			p.emit(Event{Kind: EventDone, Job: out.job, Rows: rows})
		}

		if err := ctx.Err(); err != nil {
			cancel()
			// Drain so producers observing cancellation can exit without blocking on send.
			for range outCh {
			}
			return stats, fmt.Errorf("work: run canceled: %w", err)
		}
	}

	return stats, nil
}

func (p *Pool) emit(e Event) {
	if p.OnEvent != nil {
		p.OnEvent(e)
	}
}
