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

	sup.recordSpend(expand.Stats{Processed: 12, Fetched: 3, Reused: 9, Built: 12}, nil, sup.now())

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

// Builds were the analysis count too, which billed a database code scanning supplied, or one the
// database cache held, as a build the cycle paid for.
func TestTheRecordedSpendCountsBuildsRatherThanAnalyses(t *testing.T) {
	sup, s := newSupervisor(t)

	sup.recordSpend(expand.Stats{Processed: 5, Built: 2, CodeScanning: expand.ReuseCounts{
		Databases: 3, Refused: map[string]int{}}}, nil, sup.now())

	cycles, err := s.RecentCycles(1)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(cycles) != 1 || cycles[0].Builds != 2 || cycles[0].Evaluations != 5 {
		t.Errorf("recorded %+v, want the 2 databases built and the 5 evaluations", cycles)
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

// A tree read is API budget spent in place of a clone, so a cycle that saved clones that way says
// what it paid, and the totals carry it.
func TestACycleRecordsTheTreeReadsItSpent(t *testing.T) {
	sup, s := newSupervisor(t)

	sup.recordSpend(expand.Stats{TreeReads: 4, Fetched: 1}, nil, sup.now())

	cycles, err := s.RecentCycles(1)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(cycles) != 1 || cycles[0].TreeReads != 4 {
		t.Fatalf("recorded %+v, want 4 tree reads", cycles)
	}
	total, err := s.CycleTotals()
	if err != nil {
		t.Fatalf("CycleTotals: %v", err)
	}
	if total.TreeReads != 4 {
		t.Errorf("totals carry %d tree reads, want 4", total.TreeReads)
	}
}

// The cycle line printed what code scanning supplied and nothing kept it, so Settings could not
// say why a round built what it built; a cycle now records it beside what it spent.
func TestACycleRecordsWhatCodeScanningSuppliedAndWhy(t *testing.T) {
	sup, s := newSupervisor(t)

	sup.recordSpend(expand.Stats{Processed: 3, Built: 1, CodeScanning: expand.ReuseCounts{
		Databases: 2, Analyses: 1, Refused: map[string]int{"database: another commit": 1}}}, nil, sup.now())

	cycles, err := s.RecentCycles(1)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(cycles) != 1 || cycles[0].CodeScanning == nil {
		t.Fatalf("recorded %+v, want what code scanning supplied kept", cycles)
	}
	got := *cycles[0].CodeScanning
	if got.Databases != 2 || got.Analyses != 1 || len(got.Refused) != 1 || got.Refused["database: another commit"] != 1 {
		t.Errorf("kept %+v, want 2 databases, 1 analysis and the one refusal", got)
	}
}

// A crawl that asks code scanning nothing takes nothing, which reads as refusal on the page that
// shows what each round took unless the status the page reads says why.
func TestStatusSaysWhyCodeScanningIsNotAsked(t *testing.T) {
	sup, _ := newSupervisor(t)
	sup.Expander.CodeScanningOff = "this server was started with --code-scanning-reuse=false"

	if got := sup.Status().CodeScanningUnasked; got != sup.Expander.CodeScanningOff {
		t.Errorf("status says %q, want %q", got, sup.Expander.CodeScanningOff)
	}
}
