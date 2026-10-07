package supervisor

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// newSupervisor builds a loop whose expansion claims nothing, so a cycle exercises everything
// around the analysis without a clone, a database build or a network call.
func newSupervisor(t *testing.T) (*Supervisor, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	q := &frontier.Queue{Store: s, MaxDepth: 5}
	return &Supervisor{
		Store:    s,
		Queue:    q,
		Expander: &expand.Expander{Queue: q, Store: s},
		Builder:  &graph.Builder{Store: s},
		Options:  expand.Options{QueryPath: "unused.ql", WorkDir: t.TempDir(), Limit: 0},
	}, s
}

// Nothing is running before Run is called, and a status that claimed otherwise would have an
// operator waiting on a loop that does not exist.
func TestASupervisorThatWasNeverRunReportsItselfStopped(t *testing.T) {
	sup, _ := newSupervisor(t)
	if got := sup.Status().State; got != StateStopped {
		t.Errorf("state = %q, want %q", got, StateStopped)
	}
}

// "Paused" on a loop nobody has started reads as one that was interrupted, and sends the reader
// looking for the failure that stopped it.
func TestALoopThatWasNeverStartedIsNotReportedAsPaused(t *testing.T) {
	sup, _ := newSupervisor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = sup.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sup.Status().State == StateStopped {
			return
		}
		if got := sup.Status().State; got == StatePaused {
			t.Fatalf("state = %q before anything asked it to start", got)
		}
	}
	t.Fatalf("state = %q, want %q", sup.Status().State, StateStopped)
}

// Reach nobody measured, or an older build measured without what this one shows, reads wrong on
// every page until it is recomputed; a crawl nobody started is no reason to keep showing it.
func TestALoopNobodyStartedStillMeasuresReach(t *testing.T) {
	sup, s := newSupervisor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = sup.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if version, err := s.GraphVersion(); err == nil && version.ReachRecomputedAt != "" {
			if got := sup.Status().State; got != StateStopped {
				t.Errorf("state = %q; measuring reach is not starting the crawl", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("reach was never measured while the crawl was stopped")
}

// Pressing Start clones repositories and runs CodeQL over each one. What that will cost has to
// be readable before it is spent, not inferred from the flags the server happened to get.
func TestStatusSaysWhatStartingWillSpend(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.Options.Concurrency = 4
	sup.Options.Limit = 20

	got := sup.Status()
	if got.Concurrency != 4 || got.ClaimsPerCycle != 20 || got.WorkDir == "" {
		t.Errorf("status = %+v, want the concurrency, claim budget and scratch directory", got)
	}
}

// A missing CodeQL CLI has to be answered with the command that installs it, not with a button
// that appears to work and silently does nothing.
func TestStartRefusesAndExplainsWhenPreflightFails(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.Preflight = func() *Problem {
		return &Problem{Reason: "codeql is not on PATH", Remedy: "install the CodeQL CLI"}
	}

	problem := sup.Start()
	if problem == nil {
		t.Fatal("started with a failing preflight")
	}
	if problem.Remedy == "" {
		t.Error("reported a problem with no remedy")
	}
	if sup.Status().Problem == nil {
		t.Error("the problem is not visible in the status the interface reads")
	}
	if sup.isRunning() {
		t.Error("marked itself running despite refusing to start")
	}
}

func TestStartClearsAPreviousProblem(t *testing.T) {
	sup, _ := newSupervisor(t)
	failing := true
	sup.Preflight = func() *Problem {
		if failing {
			return &Problem{Reason: "codeql is not on PATH", Remedy: "install it"}
		}
		return nil
	}

	sup.Start()
	failing = false
	if problem := sup.Start(); problem != nil {
		t.Fatalf("still refusing after the problem was fixed: %+v", problem)
	}
	if sup.Status().Problem != nil {
		t.Error("status still reports a problem that no longer applies")
	}
}

// Reach that has never been measured is the one case no threshold should defer.
func TestACycleMeasuresReachThatWasNeverMeasured(t *testing.T) {
	sup, s := newSupervisor(t)

	sup.cycle(context.Background())

	version, err := s.GraphVersion()
	if err != nil {
		t.Fatalf("GraphVersion: %v", err)
	}
	if version.Generation == 0 {
		t.Fatal("a cycle recorded no generation, so no client can tell the graph moved")
	}
	if version.ReachRecomputedAt == "" {
		t.Error("reach was never measured and the cycle did not measure it")
	}
}

// The loop is the only thing that can hand back work an interrupted run left claimed.
func TestACycleReturnsAbandonedClaimsToTheQueue(t *testing.T) {
	sup, s := newSupervisor(t)
	if _, err := sup.Queue.Enqueue([]frontier.Item{
		{NodeKey: "octo/tool", Owner: "octo", Repo: "tool", Depth: 1},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := sup.Queue.Claim(1); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	sup.Lease = time.Hour
	sup.Now = func() time.Time { return time.Now().UTC().Add(2 * time.Hour) }
	sup.Queue.Now = sup.Now
	sup.cycle(context.Background())

	var status string
	if err := s.DB().QueryRow(
		`SELECT status FROM abom_frontier WHERE node_key='octo/tool'`).Scan(&status); err != nil {
		t.Fatalf("reading the item: %v", err)
	}
	if status != frontier.StatusPending {
		t.Errorf("status = %q, want the abandoned claim back in the queue", status)
	}
	if sup.Status().Reclaimed != 1 {
		t.Errorf("Reclaimed = %d, want 1", sup.Status().Reclaimed)
	}
}

// A claim surviving process start cannot belong to a live worker, because the store admits one
// writer. Waiting out the lease costs an hour of dead time in which the interface reports work
// in flight that nothing is doing.
func TestTheFirstCycleReclaimsClaimsLeftByAPreviousProcess(t *testing.T) {
	sup, s := newSupervisor(t)
	if _, err := sup.Queue.Enqueue([]frontier.Item{
		{NodeKey: "octo/tool", Owner: "octo", Repo: "tool", Depth: 1},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := sup.Queue.Claim(1); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// Claimed moments ago, so the lease is nowhere near expiry.
	sup.Lease = time.Hour
	sup.cycle(context.Background())

	var status string
	if err := s.DB().QueryRow(
		`SELECT status FROM abom_frontier WHERE node_key='octo/tool'`).Scan(&status); err != nil {
		t.Fatalf("reading the item: %v", err)
	}
	if status != frontier.StatusPending {
		t.Errorf("status = %q, want a claim from a previous process handed straight back", status)
	}
}

// The startup sweep must not then confiscate the work this process is doing itself.
func TestLaterCyclesLeaveFreshClaimsAlone(t *testing.T) {
	sup, s := newSupervisor(t)
	sup.Lease = time.Hour
	sup.cycle(context.Background())

	if _, err := sup.Queue.Enqueue([]frontier.Item{
		{NodeKey: "octo/tool", Owner: "octo", Repo: "tool", Depth: 1},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := sup.Queue.Claim(1); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	sup.cycle(context.Background())

	var status string
	if err := s.DB().QueryRow(
		`SELECT status FROM abom_frontier WHERE node_key='octo/tool'`).Scan(&status); err != nil {
		t.Fatalf("reading the item: %v", err)
	}
	if status != frontier.StatusRunning {
		t.Errorf("status = %q, want a claim inside the lease left in flight", status)
	}
}

// A queue whose only work is waiting on a retry must not be polled tighter than the interval,
// nor treated as empty.
func TestTheLoopWaitsForWorkRatherThanSpinning(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.Interval = time.Minute
	now := time.Now().UTC()
	sup.Now = func() time.Time { return now }

	if wait := sup.waitFor(now.Add(10*time.Second), true); wait != 10*time.Second {
		t.Errorf("wait = %v, want the time until the next retry", wait)
	}
	if wait := sup.waitFor(now.Add(time.Hour), true); wait != time.Minute {
		t.Errorf("wait = %v, want the interval to cap a distant retry", wait)
	}
	if wait := sup.waitFor(time.Time{}, false); wait != time.Minute {
		t.Errorf("wait = %v, want the interval when nothing is pending", wait)
	}
}

// Status holds only the most recent error, so a failure that resolves itself before anyone looks
// leaves nothing behind unless it was written down when it happened.
func TestFailuresAreWrittenDownAndNotOnlyHeld(t *testing.T) {
	sup, _ := newSupervisor(t)
	var log bytes.Buffer
	sup.Log = &log

	sup.fail("cloning a dependency", errors.New("network unreachable"))

	if !strings.Contains(log.String(), "network unreachable") {
		t.Errorf("log = %q, want the failure recorded", log.String())
	}
	if !strings.Contains(log.String(), "cloning a dependency") {
		t.Errorf("log = %q, want what was being attempted", log.String())
	}
}

// Advisories are matched against packages by name, so a dependency discovered today is checked
// against a set fetched before it existed unless the crawl refreshes them.
func TestRefreshingAdvisoriesRecordsWhatItFound(t *testing.T) {
	sup, _ := newSupervisor(t)
	var log bytes.Buffer
	sup.Log = &log
	var asked int
	sup.Advisor = func(context.Context) (int, error) {
		asked++
		return 3, nil
	}

	sup.refreshAdvisories(context.Background())

	if asked != 1 {
		t.Errorf("advisor asked %d times, want once", asked)
	}
	if !strings.Contains(log.String(), "3 advisories") {
		t.Errorf("log = %q, want what the refresh found", log.String())
	}
}

// A drained queue never re-enqueues, so a classification only Enqueue can set keeps whatever the
// loosest rule ever said. The pass allowed to lower one has to be reached from the loop, or
// tightening the rule changes nothing anybody can see.
func TestADrainedCycleReclassifiesWhatIsAlreadyQueued(t *testing.T) {
	sup, _ := newSupervisor(t)
	var log bytes.Buffer
	sup.Log = &log
	sup.Reclassifier = func() (int, error) { return 7, nil }

	sup.cycle(context.Background())

	if !strings.Contains(log.String(), "reclassified 7 dependencies") {
		t.Errorf("log = %q, want the reclassification reported", log.String())
	}
}

// A pass that changed nothing must stay silent, or a settled corpus writes a line a minute all
// night saying so.
func TestAReclassificationThatChangedNothingIsNotAnnounced(t *testing.T) {
	sup, _ := newSupervisor(t)
	var log bytes.Buffer
	sup.Log = &log
	sup.Reclassifier = func() (int, error) { return 0, nil }

	sup.cycle(context.Background())

	if strings.Contains(log.String(), "reclassified") {
		t.Errorf("log = %q, want nothing said about an unchanged classification", log.String())
	}
}

// Refreshing on an idle cycle would spend GitHub's rate limit re-asking the same question. The
// unauthenticated budget is 60 an hour, and a loop polling all night would exhaust it for
// nothing. This is the guard in cycle, tested where it lives.
func TestAdvisoriesAreNotRefreshedWhenNothingArrived(t *testing.T) {
	sup, _ := newSupervisor(t)
	var asked int
	sup.Advisor = func(context.Context) (int, error) {
		asked++
		return 0, nil
	}

	sup.cycle(context.Background())

	if asked != 0 {
		t.Errorf("advisor asked %d times on an idle cycle, want none", asked)
	}
}

// Advisories enrich the graph; they are not what the crawl is for. A GitHub outage or an
// exhausted rate limit must not stop dependencies being analyzed.
func TestAFailedAdvisoryRefreshDoesNotStopTheCycle(t *testing.T) {
	sup, _ := newSupervisor(t)
	var log bytes.Buffer
	sup.Log = &log
	sup.Advisor = func(context.Context) (int, error) {
		return 0, errors.New("rate limit exhausted")
	}

	sup.refreshAdvisories(context.Background())

	if sup.Status().LastError == "" {
		t.Error("a failed refresh left no trace at all")
	}
	if !strings.Contains(log.String(), "rate limit exhausted") {
		t.Errorf("log does not say why the refresh failed: %q", log.String())
	}
}

// A nil Advisor is the normal configuration for anything that is not the serve loop, so it must
// be a no-op rather than a panic.
func TestARefreshWithNoAdvisorIsHarmless(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.refreshAdvisories(context.Background())
	if sup.Status().LastError != "" {
		t.Errorf("LastError = %q, want nothing recorded", sup.Status().LastError)
	}
}

// A finished crawl never ingests again, so staleness alone must refresh advisories.
func TestAStaleAdvisorySetIsRefreshedOnADrainedQueue(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.AdvisoryMaxAge = 6 * time.Hour
	sup.LastAdvisoryAt = time.Now().UTC().Add(-24 * time.Hour)
	var asked int
	sup.Advisor = func(context.Context) (int, error) {
		asked++
		return 0, nil
	}

	sup.cycle(context.Background())

	if asked != 1 {
		t.Errorf("advisor asked %d times, want once for an advisory set past its ceiling", asked)
	}
}

// Within the ceiling the answer is still good, and re-asking spends a rate limit to be told the
// same thing.
func TestAFreshAdvisorySetIsNotRefetched(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.AdvisoryMaxAge = 6 * time.Hour
	sup.LastAdvisoryAt = time.Now().UTC().Add(-time.Minute)
	var asked int
	sup.Advisor = func(context.Context) (int, error) {
		asked++
		return 0, nil
	}

	sup.cycle(context.Background())

	if asked != 0 {
		t.Errorf("advisor asked %d times inside the ceiling, want none", asked)
	}
}

// The advisory floor keeps productive cycles from re-asking about the same packages.
func TestABurstOfProductiveCyclesDoesNotRefetchAdvisoriesEachTime(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.Advisor = func(context.Context) (int, error) { return 0, nil }
	sup.AdvisoryMinInterval = 15 * time.Minute
	sup.LastAdvisoryAt = time.Now().UTC().Add(-time.Minute)

	if sup.advisoryRefreshDue(true) {
		t.Error("refetched 936 packages because one more cycle happened to ingest")
	}
}

// The floor must not become a reason never to refresh: past it, arrival still counts.
func TestArrivalRefreshesAdvisoriesOnceTheFloorHasPassed(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.Advisor = func(context.Context) (int, error) { return 0, nil }
	sup.AdvisoryMinInterval = 15 * time.Minute
	sup.LastAdvisoryAt = time.Now().UTC().Add(-time.Hour)

	if !sup.advisoryRefreshDue(true) {
		t.Error("new packages arrived well past the floor and were never checked")
	}
}

// A loop waiting on a queue whose next retry is hours away would otherwise write two lines a
// minute all night, and bury the one line that matters.
func TestAnIdleCycleIsNotLogged(t *testing.T) {
	sup, _ := newSupervisor(t)
	var log bytes.Buffer
	sup.Log = &log

	sup.cycle(context.Background())
	// The first cycle measures reach, which is worth a line; nothing after it is.
	before := log.Len()
	sup.cycle(context.Background())

	if log.Len() != before {
		t.Errorf("a cycle with nothing to do logged %q", log.String()[before:])
	}
}

// A pause takes effect between cycles, so a repository already being cloned and analyzed runs
// to completion. Reporting that as still working reads as a button that did nothing, for as long
// as the cycle takes.
func TestPausingIsVisibleWhileWorkInFlightFinishes(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.mu.Lock()
	sup.started, sup.running, sup.status.State = true, true, StateWorking
	sup.mu.Unlock()

	sup.Pause()

	if got := sup.Status().State; got != StatePausing {
		t.Errorf("state = %q while a cycle is still running, want %q", got, StatePausing)
	}
}

// Once the cycle ends there is nothing left in flight, and the honest word is "paused".
func TestPausingBecomesPausedOnceTheCycleEnds(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.mu.Lock()
	sup.started, sup.running, sup.everStarted = true, false, true
	sup.status.State = StateIdle
	sup.mu.Unlock()

	if got := sup.restingState(); got != StatePaused {
		t.Errorf("resting state = %q, want %q", got, StatePaused)
	}
}

func TestStartingAndPausingAreRecorded(t *testing.T) {
	sup, _ := newSupervisor(t)
	var log bytes.Buffer
	sup.Log = &log

	sup.Start()
	sup.Pause()

	if !strings.Contains(log.String(), "started") || !strings.Contains(log.String(), "paused") {
		t.Errorf("log = %q, want both transitions", log.String())
	}
}

// Cancellation is how this ends, so it must not be reported as a failure.
func TestRunReturnsCleanlyWhenCanceled(t *testing.T) {
	sup, _ := newSupervisor(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	sup.Start()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v on cancellation, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was canceled")
	}
	if got := sup.Status().State; got != StateStopped {
		t.Errorf("state = %q after stopping, want %q", got, StateStopped)
	}
}

// A database or an analysis taken from code scanning is a build or an evaluation the cycle did not
// pay for, and a refusal says why it paid; the line says both, and nothing when nothing was asked.
func TestACycleLineSaysWhatCodeScanningSupplied(t *testing.T) {
	took := cycleLine(3, expand.Stats{Processed: 2, CodeScanning: expand.ReuseCounts{
		Databases: 1, Refused: map[string]int{"analysis: another configuration": 1}}})
	if !strings.Contains(took, "code scanning took 1 database and 0 analyses; refused analysis: another configuration") {
		t.Errorf("line %q does not say what code scanning supplied", took)
	}
	if quiet := cycleLine(3, expand.Stats{Processed: 2}); strings.Contains(quiet, "code scanning") {
		t.Errorf("line %q mentions code scanning, which nothing asked", quiet)
	}
}
