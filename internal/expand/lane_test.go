package expand

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

// bindLanes points the harness at one graph query and one findings query, each nameable so a test
// can change one lane's identity without touching the other.
//
// Real files in **separate packs**, because both lanes are now identified by content: one pack
// holding both would have an edit to the findings query move the graph pack's digest too, and the
// isolation these tests exist to prove would be untestable. The graph pack's contents never vary,
// so its identity is the same on every call however many temporary directories are made.
func bindLanes(t *testing.T, h *harness, edgesID, findingsName string) {
	t.Helper()
	if h.laneRoot == "" {
		h.laneRoot = t.TempDir()
	}
	h.laneEdges = queryInPack(t, h.laneRoot, "advanced-security/abom-graph", "1.0.0",
		"AbomEdges.ql", "from int i select i\n")
	h.laneFindings = queryInPack(t, h.laneRoot, "codeql/actions-queries", "0.6.34",
		findingsName, "from int i select i\n")
	h.expander.Resolver = &fakeResolver{
		queries: []string{h.laneEdges, h.laneFindings},
		meta: map[string]runner.Metadata{
			h.laneEdges:    {ID: edgesID, Tags: ingest.TagEdges},
			h.laneFindings: {ID: "actions/unpinned-tag", Kind: "problem"},
		},
	}
}

func (h *harness) graphQueriesRun() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.queried)
}

// drainedLanes analyzes everything reachable from one seed through a two-lane suite, so what
// follows measures re-analysis rather than a queue still filling up with discoveries.
func drainedLanes(t *testing.T) (*harness, *fakeAnalyzer) {
	t.Helper()
	h, a := findingsHarness(t)
	h.enqueue(item("actions", "checkout", "", 1))
	for range 10 {
		if h.pending() == 0 {
			return h, a
		}
		runBound(t, h)
	}
	t.Fatalf("the queue still holds %d pending items after 10 passes", h.pending())
	return nil, nil
}

// The two lanes come from different packs with different digests, and nothing about running one
// requires running the other.
func TestAFindingsChangeDoesNotReRunTheGraphLane(t *testing.T) {
	h, a := drainedLanes(t)

	graphBefore, analyzeBefore := h.graphQueriesRun(), len(a.targets)
	if graphBefore == 0 || analyzeBefore == 0 {
		t.Fatalf("the first pass ran %d graph queries and %d analyses, want both",
			graphBefore, analyzeBefore)
	}

	// Only the findings lane moves: a different query id is a different run key.
	bindLanes(t, h, "abom/edges", "Injection.ql")
	requeued := h.requeueDirect()
	runBound(t, h)

	if got := h.graphQueriesRun() - graphBefore; got != 0 {
		t.Errorf("the graph lane ran %d queries for a findings-only change, want none", got)
	}
	if got := len(a.targets) - analyzeBefore; got == 0 {
		t.Errorf("analyze never ran for the lane that changed (%d items requeued)", requeued)
	}
}

func TestAGraphChangeDoesNotReRunTheFindingsLane(t *testing.T) {
	h, a := drainedLanes(t)

	graphBefore, analyzeBefore := h.graphQueriesRun(), len(a.targets)

	// Only the graph lane moves.
	bindLanes(t, h, "abom/edges-v2", "Unpinned.ql")
	h.requeueDirect()
	runBound(t, h)

	if got := h.graphQueriesRun() - graphBefore; got == 0 {
		t.Error("the graph lane did not run for a graph change")
	}
	if got := len(a.targets) - analyzeBefore; got != 0 {
		t.Errorf("analyze ran %d more times for a graph-only change, want none", got)
	}
}

// Skipping the edges lane leaves the round with findings and no BQRS to ingest. Consuming that
// has to be ordinary rather than an error, or the saving would come at the price of the pass.
func TestAPassWithNoEdgesToIngestStillStoresItsFindings(t *testing.T) {
	h, a := drainedLanes(t)

	before := countFindings(t, h)
	analyzeBefore := len(a.targets)

	bindLanes(t, h, "abom/edges", "Injection.ql")
	h.requeueDirect()
	stats := runBound(t, h)

	if stats.Failed != 0 {
		t.Errorf("%d repositories failed on a pass with no edges to ingest", stats.Failed)
	}
	if len(a.targets) == analyzeBefore {
		t.Fatal("the findings lane never ran, so nothing exercised the missing BQRS")
	}
	if got := countFindings(t, h); got < before {
		t.Errorf("findings fell from %d to %d across a pass with the edges lane skipped",
			before, got)
	}
}

// Both lanes on record is still the whole-repository skip, which is the cheaper case and must not
// regress into building a database to run nothing.
func TestNeitherLaneRunningStillSkipsTheDatabase(t *testing.T) {
	h, _ := drainedLanes(t)

	built := h.databasesBuilt()
	h.requeueDirect()
	runBound(t, h)

	if got := h.databasesBuilt() - built; got != 0 {
		t.Errorf("built %d databases with both lanes on record, want none", got)
	}
}

// A lane that has work to do needs a database, so the build cannot be skipped just because the
// other lane is current.
func TestALaneWithWorkStillGetsItsDatabase(t *testing.T) {
	h, _ := drainedLanes(t)

	built := h.databasesBuilt()
	bindLanes(t, h, "abom/edges", "Injection.ql")
	requeued := h.requeueDirect()
	runBound(t, h)

	if got := h.databasesBuilt() - built; got == 0 {
		t.Errorf("built no database for a lane with work (%d items requeued)", requeued)
	}
}
