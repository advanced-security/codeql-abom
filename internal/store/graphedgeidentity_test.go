package store

import (
	"fmt"
	"sort"
	"testing"
)

// The Explorer draws one line for every relationship between a pair, and a reader selecting it,
// or a delta removing one, has to be able to say which relationship they mean. The delta has
// always named edges by `edge_key`; for a long time the payload carried no key at all, so every
// removal matched nothing and silently did nothing.
func TestTheGraphNamesTheRelationshipsBehindADrawnEdge(t *testing.T) {
	s := mergedEdgeFixture(t)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	stored := storedEdgeKeys(t, s)
	named := map[string]bool{}
	for _, e := range dependencies(g.Edges) {
		if len(e.Keys) == 0 {
			t.Errorf("the edge %s -> %s names no relationship", e.Source, e.Target)
		}
		for _, k := range e.Keys {
			if !stored[k] {
				t.Errorf("the edge %s -> %s names %q, which is not a stored relationship",
					e.Source, e.Target, k)
			}
			named[k] = true
		}
	}
	for k := range stored {
		if !named[k] {
			t.Errorf("the relationship %q is drawn by no edge, so nothing can select it", k)
		}
	}
}

// Two steps of one workflow using one action are two relationships and one line. Naming either
// of them alone would make a selection or a removal act on whichever row happened to be read
// first.
func TestAMergedEdgeNamesEveryConstituentRatherThanOne(t *testing.T) {
	s := mergedEdgeFixture(t)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	e := edgeTo(t, g.Edges, "actions/checkout")
	want := []string{"pinnedcheckout", "unattributed", "usescheckout"}
	got := append([]string(nil), e.Keys...)
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("the edge names %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the edge names %v, want %v", got, want)
		}
	}
	if e.Count != len(want) {
		t.Errorf("count = %d, want %d", e.Count, len(want))
	}
}

// Keeping the first row's value reports one relationship's fact as though it were the pair's. It
// is invisible when the constituents agree, and wrong exactly when the difference matters.
func TestAFactTheConstituentsDisagreeOnIsReportedAsMixed(t *testing.T) {
	s := mergedEdgeFixture(t)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	e := edgeTo(t, g.Edges, "actions/checkout")
	for _, fact := range []string{"boundary", "refStyle"} {
		if !e.IsMixed(fact) {
			t.Errorf("%s is not reported as mixed, and the constituents disagree on it", fact)
		}
	}
	if e.Boundary != "" {
		t.Errorf("boundary = %q, want it empty: one constituent could not be classified", e.Boundary)
	}
	if e.RefStyle != "" {
		t.Errorf("refStyle = %q, want it empty: one is pinned and one is not", e.RefStyle)
	}
}

// Mixed is a last resort, not a way to avoid answering: where the relationships agree, the shared
// value is the pair's value and reporting it as mixed would hide a fact the reader can act on.
func TestAFactEveryConstituentAgreesOnIsCarried(t *testing.T) {
	s := mergedEdgeFixture(t)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	e := edgeTo(t, g.Edges, "actions/setup-node")
	if e.Kind != "step-uses-action" || e.IsMixed("kind") {
		t.Errorf("kind = %q (mixed %v), want the shared kind", e.Kind, e.Mixed)
	}
	if e.RefStyle != EdgeRefTag {
		t.Errorf("refStyle = %q, want %q: both name a version", e.RefStyle, EdgeRefTag)
	}
	if e.OwnedDepth == nil || *e.OwnedDepth != 1 {
		t.Errorf("ownedDepth = %v, want 1: the repository declares both", e.OwnedDepth)
	}
	if e.FindingRoute == nil || *e.FindingRoute {
		t.Errorf("findingRoute = %v, want a measured false", e.FindingRoute)
	}
	if e.AdvisoryRoute != nil {
		t.Errorf("advisoryRoute = %v, want nothing: no advisory is on record", *e.AdvisoryRoute)
	}
}

// A dependency analyzed at two references is drawn as two versions, and the relationship naming
// v4 belongs to v4 alone. Copying the merged edge wholesale onto each version would tell a reader
// selecting v3 that they had selected the line that pinned v4.
func TestSplittingAVersionOutKeepsOnlyTheRelationshipsThatNamedIt(t *testing.T) {
	s := mergedEdgeFixture(t)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('actions/setup-node', 'v4', 'actions', 'setup-node', 1, 'done',
		        '2026-01-01T00:00:00Z'),
		       ('actions/setup-node', 'v2', 'actions', 'setup-node', 1, 'done',
		        '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	four := edgeTo(t, g.Edges, "actions/setup-node@v4")
	two := edgeTo(t, g.Edges, "actions/setup-node@v2")
	if len(four.Keys) != 2 {
		t.Errorf("v4 names %v, want the two relationships that used it", four.Keys)
	}
	for _, k := range four.Keys {
		if k == "usesolder" {
			t.Errorf("v4 names %q, which used v2", k)
		}
	}
	if len(two.Keys) != 1 || two.Keys[0] != "usesolder" {
		t.Errorf("v2 names %v, want only the relationship that used it", two.Keys)
	}
}

// The two halves of a delta have to speak about the same things. A removal names an edge_key, so
// the payload that drew it has to carry the same key or removal is a no-op the reader never sees.
func TestADeltaNamesTheSameRelationshipsTheGraphDraws(t *testing.T) {
	s := mergedEdgeFixture(t)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	drawn := map[string]bool{}
	for _, e := range g.Edges {
		for _, k := range e.Keys {
			drawn[k] = true
		}
	}

	if _, err := s.DB().Exec(
		`UPDATE abom_edge SET removed_at = '2026-01-02T00:00:00Z', removed_generation = 1
		  WHERE edge_key = 'usesolder'`); err != nil {
		t.Fatalf("tombstoning an edge: %v", err)
	}
	removed, err := s.removedSince("abom_edge", 0)
	if err != nil {
		t.Fatalf("removedSince: %v", err)
	}
	if len(removed) == 0 {
		t.Fatal("nothing was reported as removed, so this proves nothing")
	}
	for _, k := range removed {
		if !drawn[k] {
			t.Errorf("the delta removes %q, which no drawn edge names", k)
		}
	}
}

// edgeTo finds the drawn edge into a target, failing rather than returning a zero value: a test
// asserting facts about an edge that is not there would pass on every field being empty.
func edgeTo(t *testing.T, edges []GraphEdge, target string) GraphEdge {
	t.Helper()
	for _, e := range edges {
		if e.Target == target {
			return e
		}
	}
	targets := []string{}
	for _, e := range edges {
		targets = append(targets, e.Target)
	}
	t.Fatalf("no edge into %q; the graph draws %v", target, targets)
	return GraphEdge{}
}

func storedEdgeKeys(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.DB().Query(`SELECT edge_key FROM abom_edge_live`)
	if err != nil {
		t.Fatalf("reading edge keys: %v", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("scanning edge key: %v", err)
		}
		out[key] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading edge keys: %v", err)
	}
	return out
}

// mergedEdgeFixture is one workflow whose steps produce several relationships onto each of two
// targets: one pair the constituents disagree about, one they agree about.
func mergedEdgeFixture(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	declares(t, s, "octo-org")

	app, err := s.UpsertRepository("octo-org/app", OriginSeed)
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	w := NewWriter(s, 0)
	workflow := "octo-org/app/.github/workflows/ci.yml"
	nodes := []Node{
		{Key: workflow, Kind: "workflow", RepositoryID: app},
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
			IsExternal: true},
		{Key: "actions/setup-node", Kind: "action", Owner: "actions", Repo: "setup-node",
			IsExternal: true},
	}
	for i := 0; i < 6; i++ {
		nodes = append(nodes, Node{
			Key: stepKey(i), Kind: "step", RepositoryID: app, ContainerKey: workflow,
		})
	}
	for _, n := range nodes {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}

	for i, e := range []Edge{
		{Key: "usescheckout", DstKey: "actions/checkout", RefValue: "v4", RepositoryID: app},
		{Key: "pinnedcheckout", DstKey: "actions/checkout",
			RefValue: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678", RepositoryID: app},
		// An edge whose containing repository nothing could name, which is what leaves a
		// boundary unclassified beside constituents that have one.
		{Key: "unattributed", DstKey: "actions/checkout", RefValue: "v4"},
		{Key: "usesnode", DstKey: "actions/setup-node", RefValue: "v4", RepositoryID: app},
		{Key: "usesnodeagain", DstKey: "actions/setup-node", RefValue: "v4", RepositoryID: app},
		{Key: "usesolder", DstKey: "actions/setup-node", RefValue: "v2", RepositoryID: app},
	} {
		e.SrcKey = stepKey(i)
		e.Kind = "step-uses-action"
		e.FilePath = ".github/workflows/ci.yml"
		e.StartLine = i + 1
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// Findings exist, so "no route to one" is a measurement rather than a silence.
	findingOn(t, s, app, workflow)
	classify(t, s)
	derive(t, s)
	return s
}

func stepKey(i int) string {
	return fmt.Sprintf("step:octo-org/app:ci.yml::%d", i)
}
