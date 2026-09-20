package supervisor

import (
	"context"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
)

// "Under budget" was a phrase because no cycle recorded what it cost. A loop that cannot say what
// it spent cannot be steered, and the spend is what makes advancing the depth cap a priced
// decision rather than a surprise.
func TestACycleRecordsItsSpend(t *testing.T) {
	sup, s := newSupervisor(t)
	queueOne(t, sup, "actions/checkout", 1)

	runOneCycle(sup)

	cycles, err := s.RecentCycles(10)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(cycles) == 0 {
		t.Fatal("a cycle ran and recorded nothing about what it cost")
	}
	if cycles[0].Cycle == 0 {
		t.Error("the recorded cycle has no number, so it cannot be placed in a sequence")
	}
	if cycles[0].StartedAt == "" {
		t.Error("the recorded cycle has no start time")
	}
}

// The objective has to be observable, or "complete at depth 1" is a claim nobody can check.
func TestTheRecordedCycleNamesTheDepthBeingWorked(t *testing.T) {
	sup, s := newSupervisor(t)
	queueOne(t, sup, "actions/checkout", 1)
	queueOne(t, sup, "actions/setup-go", 2)

	runOneCycle(sup)

	cycles, err := s.RecentCycles(1)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(cycles) == 0 {
		t.Fatal("no cycle was recorded")
	}
	if !cycles[0].HasOutstanding {
		t.Fatal("work is outstanding and the cycle did not say so")
	}
	if cycles[0].ShallowestIncomplete != 1 {
		t.Errorf("shallowest incomplete = %d, want 1", cycles[0].ShallowestIncomplete)
	}
}

// A complete closure is a different state from one working at depth 0, and recording it as 0
// would be an absent measurement drawn as a measurement.
func TestACompleteClosureIsRecordedAsHavingNothingOutstanding(t *testing.T) {
	sup, s := newSupervisor(t)

	runOneCycle(sup)

	cycles, err := s.RecentCycles(1)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(cycles) == 0 {
		t.Fatal("no cycle was recorded")
	}
	if cycles[0].HasOutstanding {
		t.Error("an empty queue reported outstanding work")
	}
}

// Fetches was the analysis count, which was two guesses at once: a settled unit that still had
// to clone was not counted, and a tree served from the cache was. With the cache wired into the
// loop, a cycle that cloned nothing would be billed for the whole corpus.
func TestTheRecordedSpendCountsClonesRatherThanAnalyses(t *testing.T) {
	sup, s := newSupervisor(t)

	sup.recordSpend(expand.Stats{Processed: 12, Fetched: 3, Reused: 9}, nil, sup.now())

	cycles, err := s.RecentCycles(1)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(cycles) == 0 {
		t.Fatal("no cycle was recorded")
	}
	if cycles[0].Fetches != 3 {
		t.Errorf("fetches = %d, want the 3 clones the cycle paid for", cycles[0].Fetches)
	}
	if cycles[0].Builds != 12 {
		t.Errorf("builds = %d, want 12: a reused tree is still built and evaluated",
			cycles[0].Builds)
	}
}

// The recheck was gated on a momentarily empty queue. Completeness is the honest gate: freshness
// is the third stage of the objective, so it must not spend a probe while anything at any depth
// is still unanalyzed.
func TestFreshnessWaitsUntilCompletenessIsSatisfied(t *testing.T) {
	sup, _ := newSupervisor(t)
	queueOne(t, sup, "actions/unfinished", 1)

	progress, err := sup.Queue.Completeness()
	if err != nil {
		t.Fatalf("Completeness: %v", err)
	}
	if _, outstanding := frontier.ShallowestIncomplete(progress); !outstanding {
		t.Fatal("the fixture has no outstanding work, so this proves nothing")
	}
	if freshnessDue(progress) {
		t.Error("a probe was due while depth 1 still held unanalyzed work")
	}
}

func TestFreshnessRunsOnceNothingIsOutstanding(t *testing.T) {
	sup, _ := newSupervisor(t)

	progress, err := sup.Queue.Completeness()
	if err != nil {
		t.Fatalf("Completeness: %v", err)
	}
	if !freshnessDue(progress) {
		t.Error("nothing is outstanding and freshness still did not run")
	}
}

func queueOne(t *testing.T, sup *Supervisor, key string, depth int) {
	t.Helper()
	owner, repo, _ := strings.Cut(key, "/")
	if _, err := sup.Queue.Enqueue([]frontier.Item{{
		NodeKey: key, Owner: owner, Repo: repo, Depth: depth, Ref: "v1",
	}}); err != nil {
		t.Fatalf("Enqueue(%s): %v", key, err)
	}
}

func runOneCycle(sup *Supervisor) {
	sup.cycle(context.Background())
}
