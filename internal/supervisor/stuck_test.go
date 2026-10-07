package supervisor

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
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
	if _, err := sup.Store.DB().Exec(`
		UPDATE abom_frontier
		   SET attempts = ?, reason = 'exit status 32', next_attempt_at = '2999-01-01T00:00:00Z'
		 WHERE node_key = 'octo/bootcamp'`, frontier.DefaultStuckAfter); err != nil {
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
