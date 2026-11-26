package supervisor

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

type countingProbe struct{ asked int }

func (p *countingProbe) Resolve(context.Context, string, string, string) (string, error) {
	p.asked++
	return "abc", nil
}

// stuckAtDepthZero queues an owned repository failed past the ceiling, as ghas-bootcamp was.
func stuckAtDepthZero(t *testing.T, sup *Supervisor) {
	t.Helper()
	queueOne(t, sup, "octo/bootcamp", 0)
	if _, err := sup.Store.(*store.Store).DB().Exec(`
		UPDATE abom_frontier
		   SET attempts = ?, reason = 'exit status 32', next_attempt_at = '2999-01-01T00:00:00Z'
		 WHERE node_key = 'octo/bootcamp'`, store.DefaultStuckAfter); err != nil {
		t.Fatalf("failing octo/bootcamp: %v", err)
	}
}

// Three owned repositories failing every attempt held the recheck off for 179 cycles: zero probes.
func TestTheSweepRunsWhenDepthZeroHoldsOnlyRowsPastTheCeiling(t *testing.T) {
	sup, s := newSupervisor(t)
	probe := &countingProbe{}
	sup.Rechecker = &expand.Rechecker{Queue: sup.Queue, Probe: probe}
	stuckAtDepthZero(t, sup)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
		                           resolved_sha, analyzed_ref, analyzed_at, analysis_count)
		VALUES ('actions/checkout', 'v4', 'actions', 'checkout', 1, 'done',
		        '2026-01-01T00:00:00Z', 'abc', 'v4', '2026-01-01T00:00:00Z', 1)`); err != nil {
		t.Fatalf("recording an analyzed dependency: %v", err)
	}

	runOneCycle(sup)

	if probe.asked == 0 {
		t.Error("the recheck sweep never probed while depth 0 held only a row past the ceiling")
	}
}

// A changed rule never reached what was queued while a stuck row kept the queue from draining.
func TestOnlyStuckWorkStillLetsTheDrainedQueuePassesRun(t *testing.T) {
	sup, _ := newSupervisor(t)
	var log bytes.Buffer
	sup.Log = &log
	sup.Reclassifier = func() (int, error) { return 2, nil }
	swept := false
	sup.Sweeper = func() (int, error) { swept = true; return 0, nil }
	stuckAtDepthZero(t, sup)

	runOneCycle(sup)

	if !strings.Contains(log.String(), "reclassified 2 dependencies") {
		t.Errorf("log = %q, want the reclassification to have run", log.String())
	}
	if !swept {
		t.Error("the drained-queue sweep did not run while only a row past the ceiling was pending")
	}
}

// Work that can still be tried keeps the queue open, or the passes would run mid-crawl.
func TestUntriedWorkStillHoldsTheDrainedQueuePassesOff(t *testing.T) {
	sup, _ := newSupervisor(t)
	swept := false
	sup.Sweeper = func() (int, error) { swept = true; return 0, nil }
	stuckAtDepthZero(t, sup)
	queueOne(t, sup, "octo/untried", 0)

	runOneCycle(sup)

	if swept {
		t.Error("the drained-queue sweep ran with untried work still queued")
	}
}

// unavailableDependency records a dependency whose clone was refused, probed last at checked.
func unavailableDependency(t *testing.T, sup *Supervisor, checked any) {
	t.Helper()
	if _, err := sup.Store.(*store.Store).DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at, completed_at,
		                           checked_at, reason)
		VALUES ('octo/lib', 'v1', 'octo', 'lib', 2, 'unavailable', '2026-01-01T00:00:00Z',
		        '2026-01-01T00:00:00Z', ?, 'dependency unavailable: cloning https://github.com/octo/lib.git at "v1": remote: Repository not found.')`,
		checked); err != nil {
		t.Fatalf("recording an unavailable dependency: %v", err)
	}
}

// A dependency comes back without an event to say so, and its return closes a gap in coverage,
// so it is probed while the crawl still has unanalyzed work rather than once it is complete.
func TestAnUnavailableDependencyThatAnswersAgainIsQueuedWhileTheCrawlIsIncomplete(t *testing.T) {
	sup, s := newSupervisor(t)
	var log bytes.Buffer
	sup.Log = &log
	probe := &countingProbe{}
	sup.Rechecker = &expand.Rechecker{Queue: sup.Queue, Probe: probe}
	queueOne(t, sup, "octo/untried", 0)
	unavailableDependency(t, sup, nil)

	runOneCycle(sup)

	var status string
	if err := s.DB().QueryRow(`SELECT status FROM abom_frontier WHERE node_key = 'octo/lib'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if probe.asked != 1 || status != frontier.StatusPending {
		t.Errorf("probed %d times and left octo/lib %q; want one probe putting it back", probe.asked, status)
	}
	if !strings.Contains(log.String(), "probed 1 unavailable references; 1 answered again") {
		t.Errorf("log = %q, want the return said", log.String())
	}
}

// An installation is when access arrives, so an account it names is probed at once, whatever the
// daily clock says; nothing else probes a reference that was probed within the day.
func TestAnAccountAnInstallationNamesIsProbedBeforeItsDay(t *testing.T) {
	sup, _ := newSupervisor(t)
	probe := &countingProbe{}
	sup.Rechecker = &expand.Rechecker{Queue: sup.Queue, Probe: probe}
	unavailableDependency(t, sup, sup.now().Format("2006-01-02T15:04:05Z07:00"))

	runOneCycle(sup)
	if probe.asked != 0 {
		t.Fatalf("a reference probed within the day was probed %d times", probe.asked)
	}
	sup.ReprobeSoon("Octo")
	runOneCycle(sup)
	if probe.asked != 1 {
		t.Errorf("probed %d times after an installation named octo, want 1", probe.asked)
	}
}

// An account an installation named is probed once a probe completes: one that stopped part-way keeps
// the ask for the next, rather than leaving it to the daily clock.
func TestAnAskedProbeThatStoppedPartWayIsAskedAgain(t *testing.T) {
	sup, _ := newSupervisor(t)
	probe := &countingProbe{}
	sup.Rechecker = &expand.Rechecker{Queue: sup.Queue, Probe: probe}
	unavailableDependency(t, sup, sup.now().Format("2006-01-02T15:04:05Z07:00"))

	sup.ReprobeSoon("Octo")
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sup.reprobe(stopped); err == nil {
		t.Fatal("a probe stopped before it began reported no error")
	}
	if err := sup.reprobe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if probe.asked != 1 {
		t.Errorf("probed %d times after the first attempt stopped; want octo probed once, by the next", probe.asked)
	}
}
