package supervisor

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Supervisor runs discovery continuously instead of once per invocation.
//
// Expansion is recursive and long: analyzing a dependency reveals more dependencies, and a real
// corpus takes far longer than anyone will sit and watch. The loop therefore has to survive its
// own failures, hand abandoned work back, and keep saying what it is doing.
type Supervisor struct {
	Store     *store.Store
	Queue     *frontier.Queue
	Expander  *expand.Expander
	Rechecker *expand.Rechecker
	Builder   *graph.Builder
	Options   expand.Options

	// Interval is the longest a cycle will wait before looking again.
	Interval time.Duration
	// Lease bounds how long a claim may sit before another cycle may take it.
	Lease        time.Duration
	RecheckTTL   time.Duration
	RecheckLimit int
	Aggregates   AggregatePolicy

	// Preflight reports why discovery cannot run, or nil. Consulted before starting so a
	// missing tool is answered with its remedy rather than a button that does nothing.
	Preflight func() *Problem
	// Sweeper re-checks what should be queued against everything already analyzed, and reports
	// how much it added. Run only on a drained queue, where it is cheap and where a rule that
	// has since changed would otherwise never reach an existing database. Nil disables it.
	Sweeper func() (int, error)
	Now     func() time.Time
	// Log receives one line per event worth a human's attention. Nil discards them.
	//
	// A loop meant to run for hours has to leave a trail: Status carries only the most recent
	// error, so a failure that resolves itself before anyone looks leaves nothing behind.
	Log io.Writer

	mu           sync.Mutex
	status       Status
	running      bool
	wake         chan struct{}
	started      bool
	everStarted  bool
	sweptOnStart bool
}

// Default loop settings.
const (
	DefaultInterval     = 30 * time.Second
	DefaultLease        = time.Hour
	DefaultRecheckTTL   = 24 * time.Hour
	DefaultRecheckLimit = 50
)

func (s *Supervisor) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Supervisor) interval() time.Duration {
	if s.Interval <= 0 {
		return DefaultInterval
	}
	return s.Interval
}

func (s *Supervisor) lease() time.Duration {
	if s.Lease <= 0 {
		return DefaultLease
	}
	return s.Lease
}

// reclaimLease is zero for the first cycle of a process: the store admits a single writer, so a
// claim that outlived the process holding it cannot be in flight anywhere, and honoring the lease
// would only hide that work behind an hour of apparent progress.
func (s *Supervisor) reclaimLease() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sweptOnStart {
		s.sweptOnStart = true
		return 0
	}
	return s.lease()
}

func (s *Supervisor) recheckTTL() time.Duration {
	if s.RecheckTTL <= 0 {
		return DefaultRecheckTTL
	}
	return s.RecheckTTL
}

func (s *Supervisor) recheckLimit() int {
	if s.RecheckLimit <= 0 {
		return DefaultRecheckLimit
	}
	return s.RecheckLimit
}

// Start asks the loop to work. Starting an already-running loop is a no-op rather than a second
// loop, which would put two workers on one SQLite writer.
func (s *Supervisor) Start() *Problem {
	if s.Preflight != nil {
		if problem := s.Preflight(); problem != nil {
			s.mu.Lock()
			s.status.Problem = problem
			s.mu.Unlock()
			s.logf("cannot start: %s (%s)", problem.Reason, problem.Remedy)
			return problem
		}
	}
	s.mu.Lock()
	s.status.Problem = nil
	already := s.running
	s.running = true
	s.everStarted = true
	s.mu.Unlock()
	if !already {
		s.signal()
		s.logf("started")
	}
	return nil
}

// Pause stops claiming new work. Anything already in flight finishes.
func (s *Supervisor) Pause() {
	s.mu.Lock()
	was := s.running
	s.running = false
	s.mu.Unlock()
	if was {
		s.logf("paused")
	}
	s.signal()
}

// logf records one line, timestamped, so a trail read hours later can be placed in time.
func (s *Supervisor) logf(format string, args ...any) {
	if s.Log == nil {
		return
	}
	fmt.Fprintf(s.Log, "%s discovery: %s\n",
		s.now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// Wake asks a running loop to look for work now rather than at the end of its interval. It does
// not start a paused loop: work appearing is not a reason to override someone having stopped it.
func (s *Supervisor) Wake() { s.signal() }

func (s *Supervisor) signal() {
	s.mu.Lock()
	ch := s.wake
	s.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *Supervisor) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// restingState tells a loop that was paused from one that has never been asked to do anything.
func (s *Supervisor) restingState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.everStarted {
		return StatePaused
	}
	return StateStopped
}

// Run drives the loop until the context is canceled. It returns nil on cancellation: stopping is
// how this ends, not a failure.
func (s *Supervisor) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
	}
	s.started = true
	wake := s.wake
	s.mu.Unlock()
	defer s.setState(StateStopped)

	for {
		if ctx.Err() != nil {
			return nil
		}
		if !s.isRunning() {
			s.setState(s.restingState())
			select {
			case <-ctx.Done():
				return nil
			case <-wake:
				continue
			}
		}

		wait := s.cycle(ctx)
		if ctx.Err() != nil {
			return nil
		}
		s.setState(StateIdle)

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// cycle does one round of work and reports how long to wait before the next.
//
// It never returns an error: a failure inside one cycle is recorded and the loop continues,
// because the alternative is a background process that dies silently hours after anyone looked.
func (s *Supervisor) cycle(ctx context.Context) time.Duration {
	s.beginCycle()

	if n, err := s.Queue.ReclaimStale(s.reclaimLease()); err != nil {
		return s.fail("reclaiming abandoned work", err)
	} else if n > 0 {
		s.record(func(st *Status) { st.Reclaimed += n })
		s.logf("reclaimed %d dependencies left claimed by an interrupted run", n)
	}

	s.setState(StateWorking)
	stats, err := s.Expander.Run(ctx, s.Options)
	s.record(func(st *Status) {
		st.Analyzed += stats.Processed
		st.Settled += stats.Items
		st.Failed += stats.Failed
		st.Unavailable += stats.Unavailable
		st.Discovered += stats.Discovered
		st.Edges += stats.Edges
	})
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		return s.fail("expanding dependencies", err)
	}

	ingested := stats.Edges > 0 || stats.Processed > 0
	if ingested {
		// Only when something happened: a loop waiting on a queue whose next retry is hours
		// away would otherwise write two lines a minute all night.
		s.logf("cycle %d analyzed %d repositories, settled %d dependencies, "+
			"discovered %d, ingested %d edges (%d failed, %d unavailable)",
			s.Status().Cycle, stats.Processed, stats.Items, stats.Discovered, stats.Edges,
			stats.Failed, stats.Unavailable)
		if _, err := s.Store.LinkFindingsToNodes(); err != nil {
			return s.fail("attributing findings", err)
		}
	}

	next, pending, err := s.Queue.NextAttemptAt()
	if err != nil {
		return s.fail("reading the queue", err)
	}
	drained := !pending

	// A drained queue is the moment to check the rule against everything already known, rather
	// than only against whatever happened to be analyzed this cycle.
	if drained && s.Sweeper != nil {
		n, err := s.Sweeper()
		if err != nil {
			return s.fail("re-checking what should be queued", err)
		}
		if n > 0 {
			s.record(func(st *Status) { st.Discovered += n })
			s.logf("queue drained: %d dependencies were reachable but had never been queued", n)
			next, pending, err = s.Queue.NextAttemptAt()
			if err != nil {
				return s.fail("reading the queue", err)
			}
			drained = !pending
		}
	}

	if err := s.refreshAggregates(drained, ingested); err != nil {
		return s.fail("recomputing aggregates", err)
	}
	if err := s.Queue.Rescore(); err != nil {
		return s.fail("rescoring the queue", err)
	}
	if err := s.recheck(ctx, drained); err != nil && ctx.Err() == nil {
		return s.fail("rechecking analyzed dependencies", err)
	}

	return s.waitFor(next, pending)
}

// refreshAggregates recomputes reach when the policy says it is worth the cost, and records a
// generation whenever the graph moved so the interface can tell.
func (s *Supervisor) refreshAggregates(drained, ingested bool) error {
	staleness, err := s.Store.ReachStaleness()
	if err != nil {
		return err
	}
	due, why := s.Aggregates.due(staleness, drained, s.now())
	if !due {
		if ingested {
			if _, err := s.Store.RecordGraphGeneration(store.GraphRebuild{}); err != nil {
				return err
			}
		}
		s.record(func(st *Status) { st.ReachPending = staleness.NewEdges })
		return nil
	}

	started := time.Now()
	if err := s.Builder.RebuildAggregates(); err != nil {
		return err
	}
	elapsed := time.Since(started)

	version, err := s.Store.GraphVersion()
	if err != nil {
		return err
	}
	if _, err := s.Store.RecordGraphGeneration(store.GraphRebuild{
		ReachRebuilt: true, Duration: elapsed,
		Nodes: version.Nodes, Edges: version.Edges,
	}); err != nil {
		return err
	}
	s.record(func(st *Status) {
		st.ReachPending = 0
		st.LastRebuildReason = why
		st.LastRebuildMillis = elapsed.Milliseconds()
	})
	s.logf("recomputed reach in %dms because %s", elapsed.Milliseconds(), why)
	return nil
}

// recheck probes analyzed dependencies for ref drift once there is nothing better to do. It is
// deliberately last: re-analyzing what moved matters less than analyzing what was never seen.
func (s *Supervisor) recheck(ctx context.Context, drained bool) error {
	if s.Rechecker == nil || !drained {
		return nil
	}
	stats, err := s.Rechecker.Run(ctx, s.recheckTTL(), s.recheckLimit())
	s.record(func(st *Status) {
		st.Rechecked += stats.Checked
		st.Drifted += stats.Drifted
	})
	if stats.Checked > 0 {
		s.logf("probed %d analyzed dependencies for drift, %d had moved",
			stats.Checked, stats.Drifted)
	}
	return err
}

// waitFor sleeps until there is work, rather than polling a queue whose next item is hours away.
func (s *Supervisor) waitFor(next time.Time, pending bool) time.Duration {
	if !pending {
		return s.interval()
	}
	until := next.Sub(s.now())
	if until <= 0 {
		return 0
	}
	if until > s.interval() {
		return s.interval()
	}
	return until
}
