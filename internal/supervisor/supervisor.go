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
	// Reclassifier re-derives how queued dependencies are classified from the whole graph, and
	// reports how many it changed. It is the only pass allowed to lower a claim, because Enqueue
	// may only ever strengthen one; without it a tightened rule never reaches an existing
	// database. Nil disables it.
	Reclassifier func() (int, error)
	// Advisor refreshes advisories for the packages the graph now names, reporting how many were
	// recorded. Nil disables it.
	Advisor func(context.Context) (int, error)
	// AdvisoryMaxAge bounds how stale the advisory set may get once the crawl stops growing.
	//
	// Arrival alone is not enough: a finished corpus never ingests again, so a refresh keyed only
	// on discovery would leave analyzed repositories matched forever against whatever was fetched
	// the day the queue drained. Zero disables the ceiling.
	AdvisoryMaxAge time.Duration
	// AdvisoryMinInterval floors how often the set is refetched, however much arrives.
	//
	// The refresh runs inside the cycle, and the loop analyzes nothing meanwhile. Without a floor
	// a crawl ingesting on every cycle would spend most of its time re-asking the same question.
	AdvisoryMinInterval time.Duration
	// LastAdvisoryAt seeds the ceiling from what the store already holds, so a restart does not
	// re-fetch a set collected minutes ago.
	LastAdvisoryAt time.Time

	// Enumerate re-reads the declared accounts, reporting how many repositories it queued.
	//
	// Without it the loop can only work on what something else already recorded, so a repository
	// created after the last enumeration is invisible to it however long it runs. Nil keeps that
	// behavior, which is what every build before this one did.
	Enumerate func(context.Context) (int, error)
	// EnumerateEvery bounds how often the organization is re-read. Zero uses the default.
	//
	// A full listing is cheap against an unchanged account — its pages are conditional, so they
	// cost a round trip and no quota — but it is not free, and a cycle runs every thirty seconds.
	// Reading it more cleverly than this is the sweep, which docs/architecture.md describes.
	EnumerateEvery time.Duration
	// LastEnumeratedAt is when the accounts were last read, successfully or not. A failed attempt
	// counts, or an unreachable account is retried on every cycle forever.
	LastEnumeratedAt time.Time

	Now func() time.Time
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
	// Status counters are cumulative since the process started, so a cycle's own spend is the
	// difference from what the previous cycle had already counted.
	lastRechecked int
	lastDrifted   int
}

// Default loop settings.
const (
	DefaultInterval     = 30 * time.Second
	DefaultLease        = time.Hour
	DefaultRecheckTTL   = 24 * time.Hour
	DefaultRecheckLimit = 50
	// DefaultAdvisoryMaxAge matches the aggregate ceiling: new advisories are published daily,
	// and package advisory lookups are batched.
	DefaultAdvisoryMaxAge = 24 * time.Hour
	// DefaultAdvisoryMinInterval keeps a productive crawl from refreshing on every cycle.
	DefaultAdvisoryMinInterval = 15 * time.Minute
	// DefaultEnumerateEvery bounds how stale the list of repositories may get. An hour is short
	// enough that a repository created in the morning is analyzed the same day, and long enough
	// that the listing is a rounding error beside the analysis.
	DefaultEnumerateEvery = time.Hour
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

// cycleProgressed reports whether a cycle is worth a log line. A re-analysis that finds
// everything already on record still settles every repository it claimed, and staying silent
// through that reads as a hung crawl.
func cycleProgressed(s expand.Stats) bool {
	return s.Edges > 0 || s.Processed > 0 || s.Skipped > 0
}

// cycleIngested reports whether a cycle produced new graph data. Skipping produces none, so the
// work that follows ingestion has nothing to chew on.
func cycleIngested(s expand.Stats) bool {
	return s.Edges > 0 || s.Processed > 0
}

// recordCycle folds a round into the running totals the interface reads. Named rather than
// inlined so every count the cycle log reports has somewhere to land, which Skipped did not.
func recordCycle(st *Status, s expand.Stats) {
	st.Analyzed += s.Processed
	st.Skipped += s.Skipped
	st.Reused += s.Reused
	st.Settled += s.Items
	st.Failed += s.Failed
	st.Unavailable += s.Unavailable
	st.Discovered += s.Discovered
	st.Edges += s.Edges
}

// readOrganization re-reads the declared accounts when the period has elapsed.
//
// A failure is recorded and the cycle continues: an account that could not be reached is a
// coverage gap, and stopping the analysis already queued would turn one unreachable account into
// no progress at all.
func (s *Supervisor) readOrganization(ctx context.Context) {
	if s.Enumerate == nil || !s.enumerationDue() {
		return
	}
	// Recorded before the attempt, so an account that fails is not retried every thirty seconds.
	s.LastEnumeratedAt = s.now()

	queued, err := s.Enumerate(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		s.record(func(st *Status) {
			st.LastError = fmt.Sprintf("reading the organization: %v", err)
			st.LastErrorAt = s.now().Format(time.RFC3339)
		})
		s.logf("could not read the organization: %v", err)
		return
	}
	if queued > 0 {
		s.logf("read the organization and queued %d repositories", queued)
	}
}

// enumerationDue reports whether the organization is worth re-reading.
func (s *Supervisor) enumerationDue() bool {
	if s.LastEnumeratedAt.IsZero() {
		return true
	}
	every := s.EnumerateEvery
	if every <= 0 {
		every = DefaultEnumerateEvery
	}
	return s.now().Sub(s.LastEnumeratedAt) >= every
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
	s.rememberIntent(true)
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
	s.rememberIntent(false)
	s.signal()
}

// rememberIntent records what the operator asked for, so a restart does not undo it.
//
// A failure to write is logged and not returned: the loop is already doing what was asked, and
// refusing to start because a preference could not be saved would be the worse of the two.
func (s *Supervisor) rememberIntent(running bool) {
	if s.Store == nil {
		return
	}
	if err := s.Store.SetDiscoveryIntent(running); err != nil {
		s.logf("could not record that discovery should be running=%v: %v", running, err)
	}
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

// RequeueStale and RequeueRepository belong here because the interface reaches the selection
// through the loop, and the loop is what holds the expansion that can resolve a pack identity.
func (s *Supervisor) RequeueStale(ctx context.Context) (expand.Selection, error) {
	return s.Expander.RequeueStale(ctx)
}

func (s *Supervisor) RequeueRepository(name string) (expand.Selection, error) {
	return s.Expander.RequeueRepository(name)
}

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
			// Measuring reach is not crawling: what an older build measured, or nothing did, reads
			// wrong on every page until it is recomputed, whether or not anybody starts the crawl.
			if err := s.refreshAggregates(false, false); err != nil {
				s.logf("could not recompute reach: %v", err)
			}
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
	started := s.now()

	if n, err := s.Queue.ReclaimStale(s.reclaimLease()); err != nil {
		return s.fail("reclaiming abandoned work", err)
	} else if n > 0 {
		s.record(func(st *Status) { st.Reclaimed += n })
		s.logf("reclaimed %d dependencies left claimed by an interrupted run", n)
	}

	// Before expansion, not after: a repository found now should be queued before this cycle
	// claims its work, rather than waiting for the next one.
	s.readOrganization(ctx)

	// Before the claim too, so what is put back is analyzed this cycle rather than the next.
	if err := s.reobserve(); err != nil {
		return s.fail("putting back commits whose declarations are not known", err)
	}

	s.setState(StateWorking)
	stats, err := s.Expander.Run(ctx, s.Options)
	s.record(func(st *Status) { recordCycle(st, stats) })
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		return s.fail("expanding dependencies", err)
	}

	ingested := cycleIngested(stats)
	if cycleProgressed(stats) {
		// Only when something happened: a loop waiting on a queue whose next retry is hours
		// away would otherwise write two lines a minute all night.
		s.logf("cycle %d analyzed %d repositories, skipped %d already on record, settled %d "+
			"dependencies, discovered %d, ingested %d edges (%d fetched, %d reused, %d failed, "+
			"%d unavailable)",
			s.Status().Cycle, stats.Processed, stats.Skipped, stats.Items, stats.Discovered,
			stats.Edges, stats.Fetched, stats.Reused, stats.Failed, stats.Unavailable)
	}
	if ingested {
		if _, err := s.Store.LinkFindingsToNodes(); err != nil {
			return s.fail("attributing findings", err)
		}
	}
	if s.advisoryRefreshDue(ingested) {
		s.refreshAdvisories(ctx)
	}

	next, pending, err := s.Queue.NextAttemptAt()
	if err != nil {
		return s.fail("reading the queue", err)
	}
	drained := !pending

	// Classification is a whole-graph judgement, so it is re-derived here rather than from
	// whatever this cycle happened to analyze. It runs before the sweep because anything the
	// sweep then enqueues is classified by the same rule on the way in.
	if drained && s.Reclassifier != nil {
		n, err := s.Reclassifier()
		if err != nil {
			return s.fail("reclassifying queued dependencies", err)
		}
		if n > 0 {
			s.logf("reclassified %d dependencies against what the seed repositories reach", n)
		}
	}

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

	progress, err := s.Queue.Completeness()
	if err != nil {
		return s.fail("reading completeness", err)
	}
	if err := s.recheck(ctx, progress); err != nil && ctx.Err() == nil {
		return s.fail("rechecking analyzed dependencies", err)
	}
	// After the recheck, so the probes it spent are in the cycle's own accounting rather than
	// the next one's.
	s.recordSpend(stats, progress, started)

	return s.waitFor(next, pending)
}

// recordSpend appends what this cycle cost and what it bought.
//
// Failures are logged and not returned: losing a line of accounting is not a reason to fail a
// cycle whose work already succeeded.
func (s *Supervisor) recordSpend(stats expand.Stats, progress []frontier.DepthProgress,
	started time.Time) {
	st := s.Status()
	spend := store.CycleSpend{
		Cycle:     st.Cycle,
		StartedAt: started.UTC().Format(time.RFC3339Nano),
		Millis:    s.now().Sub(started).Milliseconds(),
		// A repository is built and evaluated once per analysis, so those are the analysis count
		// rather than separately instrumented counters. Fetches is not: a checkout served from
		// the cache costs nothing, and a settled unit that still had to clone costs one, so it is
		// counted where the clone happens.
		Fetches:     stats.Fetched,
		Builds:      stats.Processed,
		Evaluations: stats.Processed,
		Probes:      st.Rechecked - s.lastRechecked,
		Analyzed:    stats.Processed,
		Skipped:     stats.Skipped,
		Settled:     stats.Items,
		Discovered:  stats.Discovered,
		Drifted:     st.Drifted - s.lastDrifted,
	}
	spend.ShallowestIncomplete, spend.HasOutstanding = frontier.ShallowestIncomplete(progress)
	s.lastRechecked, s.lastDrifted = st.Rechecked, st.Drifted

	if err := s.Store.RecordCycle(spend); err != nil {
		s.logf("could not record what cycle %d spent: %v", spend.Cycle, err)
	}
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

// advisoryRefreshDue reports whether the advisory question has changed: either new packages
// arrived, or the existing answer has aged past the ceiling. The floor outranks both.
func (s *Supervisor) advisoryRefreshDue(ingested bool) bool {
	if s.Advisor == nil {
		return false
	}
	since := s.now().Sub(s.LastAdvisoryAt)
	if s.AdvisoryMinInterval > 0 && since < s.AdvisoryMinInterval {
		return false
	}
	if ingested {
		return true
	}
	if s.AdvisoryMaxAge <= 0 {
		return false
	}
	return since >= s.AdvisoryMaxAge
}

// refreshAdvisories brings the advisory set up to date with the packages just discovered.
//
// A failure is recorded and the cycle continues. Advisories enrich the graph; they are not what
// the crawl is for, and a GitHub outage or an exhausted rate limit must not stop dependencies
// being analyzed.
func (s *Supervisor) refreshAdvisories(ctx context.Context) {
	if s.Advisor == nil {
		return
	}
	n, err := s.Advisor(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		s.fail("refreshing advisories", err)
		return
	}
	// Recorded even when nothing came back: the question was asked, and asking again immediately
	// would spend the rate limit to be told the same thing.
	s.LastAdvisoryAt = s.now()
	if n > 0 {
		s.logf("refreshed %d advisories for the packages now in the graph", n)
	}
}

// reobserve returns analyzed dependencies whose commit's declarations are not known to the queue,
// one claim's worth at a time, so the crawl analyzes those commits again.
//
// Claimable work rather than freshness: until a commit is observed, a walk through it can miss a
// route it declares and a submission reading it is refused. That is a gap in what is known, and
// freshness waits on every gap being closed.
func (s *Supervisor) reobserve() error {
	got, err := s.Store.Reobserve(s.Options.Limit)
	if err != nil {
		return err
	}
	if got.Requeued > 0 {
		s.logf("put back %d analyzed dependencies to observe what their commits declared; "+
			"%d commits are not yet observed", got.Requeued, got.Unobserved)
	}
	return nil
}

// recheck probes analyzed dependencies for ref drift once completeness is satisfied. It is
// deliberately last: re-analyzing what moved matters less than analyzing what was never seen.
func (s *Supervisor) recheck(ctx context.Context, progress []frontier.DepthProgress) error {
	if s.Rechecker == nil || !freshnessDue(progress) {
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

// freshnessDue reports whether the objective has reached its third stage.
//
// Gated on completeness rather than on a momentarily empty queue: `drained` means nothing is
// claimable *right now*, which is transiently true while work waits on a retry, and freshness
// must not spend a probe while anything at any depth has never been analyzed.
func freshnessDue(progress []frontier.DepthProgress) bool {
	_, outstanding := frontier.ShallowestIncomplete(progress)
	return !outstanding
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
