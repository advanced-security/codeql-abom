package store

import "testing"

// A predicate the API accepts and does not apply is worse than one it refuses.
//
// `origins`, `minReach` and the unanalyzed flag parsed cleanly and changed nothing: measured on
// the reference corpus 2026-09-11, `/api/graph/full?origins=seed` returned all **12,550 nodes and
// 35,384 edges**, identical to no predicate at all. A gate asking for one tier would have been
// handed every tier and no way to tell. These tests pin each predicate to a visible effect.

func TestTheGraphKeepsOnlyTheOriginsAskedFor(t *testing.T) {
	g, err := subgraphFixture(t).FullGraph(FullGraphOptions{Origins: []string{OriginSeed}})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := keysOf(g)
	if !keys["octo-org/app/.github/workflows/ci.yml"] {
		t.Error("a seed repository's workflow was dropped by origins=seed")
	}
	for _, gone := range []string{
		"octo-org/shared/.github/workflows/ci.yml", // upstream
		"actions/checkout",                         // no repository at all
	} {
		if keys[gone] {
			t.Errorf("%s is not seed and survived origins=seed", gone)
		}
	}
}

// A node with no repository has no origin column to read, and the browser reads it as `external`.
// The server has to agree, or the same predicate names a different set on each side.
func TestANodeWithNoRepositoryCountsAsExternalOrigin(t *testing.T) {
	g, err := subgraphFixture(t).FullGraph(FullGraphOptions{Origins: []string{"external"}})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	if !keysOf(g)["actions/checkout"] {
		t.Error("a node with no repository was not treated as external")
	}
}

// Filtering nodes must take their edges with them. The graph is bipartite by kind, so a predicate
// that kept a dangling edge would render an endpoint that is not in the payload.
func TestAPredicateTakesTheEdgesOfTheNodesItRemoves(t *testing.T) {
	g, err := subgraphFixture(t).FullGraph(FullGraphOptions{Origins: []string{OriginSeed}})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := keysOf(g)
	for _, e := range g.Edges {
		if !keys[e.Source] || !keys[e.Target] {
			t.Errorf("edge %s -> %s survived with an end the predicate removed", e.Source, e.Target)
		}
	}
}

// A floor on reach excludes a node whose reach nobody has measured, which is the conservative
// direction: the alternative claims a reach the rebuild has never established.
func TestTheReachFloorKeepsOnlyWhatMeetsItAndExcludesTheUnmeasured(t *testing.T) {
	s := subgraphFixture(t)
	var checkout int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM abom_node WHERE node_key = 'actions/checkout'`).Scan(&checkout); err != nil {
		t.Fatalf("finding the node to measure: %v", err)
	}
	if _, err := s.DB().Exec(`INSERT INTO abom_blast_radius
		(node_row_id, dependent_repos, dependent_workflows, direct_dependents, max_depth)
		VALUES (?, 3, 3, 3, 1)`, checkout); err != nil {
		t.Fatalf("recording reach: %v", err)
	}

	g, err := s.FullGraph(FullGraphOptions{MinReach: 3})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := keysOf(g)
	if !keys["actions/checkout"] {
		t.Error("a node reaching exactly the floor was dropped")
	}
	if keys["octo-org/app/.github/workflows/ci.yml"] {
		t.Error("a node whose reach was never measured passed a floor of 3")
	}
}

// The browser's rule, which the server now has to mean too: a dependency is analyzed only when
// every reference in use has been. Zero references is nothing looked at, not everything covered.
//
// **This is why the predicate cannot be pushed into SQL.** Queueing a dependency at two references
// splits it into two nodes, and `nodeAtRef` gives each its own analysis state — so the question is
// asked of a node that does not exist until after the query has run. Filtering the unsplit row
// would keep `@v4`, which the browser hides.
func TestTheUnanalyzedPredicateKeepsWhatIsNotCoveredAtEveryReference(t *testing.T) {
	s := subgraphFixture(t)
	for _, row := range []struct {
		key, ref, status string
	}{
		{"actions/checkout", "v4", "done"},
		{"actions/checkout", "v3", "pending"},
	} {
		if _, err := s.DB().Exec(`INSERT INTO abom_frontier
			(node_key, ref, owner, repo, depth, status, score, enqueued_at)
			VALUES (?, ?, 'actions', 'checkout', 1, ?, 1, '2026-01-01T00:00:00Z')`,
			row.key, row.ref, row.status); err != nil {
			t.Fatalf("queuing %s@%s: %v", row.key, row.ref, err)
		}
	}

	g, err := s.FullGraph(FullGraphOptions{UnanalyzedOnly: true})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := keysOf(g)
	if !keys["actions/checkout@v3"] {
		t.Error("the reference nothing has analyzed was dropped by unanalyzedOnly")
	}
	if keys["actions/checkout@v4"] {
		t.Error("a reference analyzed at every use survived unanalyzedOnly")
	}
}

// A predicate must not be allowed to delete the nodes it is selecting.
//
// This is the defect the real corpus found and the unit tests did not. Filtering by origin in SQL
// removed the seed and upstream *steps* that point at external actions — and those edges are what
// `refsInUse` reads to decide which references a dependency exists at. With the edges gone the
// split produced no references and the dependency vanished outright: **1,889 of 3,433 action nodes
// absent**, and the three origin buckets summing to 11,045 against a corpus of 12,550.
//
// The scope filter survives in SQL only because a closure is edge-closed by construction. An
// origin is not. So the predicate runs after the split, and this asserts the ordering rather than
// the mechanism.
func TestSelectingOneTierDoesNotDeleteTheVersionsOfAnotherTiersDependencies(t *testing.T) {
	s := subgraphFixture(t)
	for _, ref := range []string{"v4", "v3"} {
		if _, err := s.DB().Exec(`INSERT INTO abom_frontier
			(node_key, ref, owner, repo, depth, status, score, enqueued_at)
			VALUES ('actions/checkout', ?, 'actions', 'checkout', 1, 'done', 1,
			        '2026-01-01T00:00:00Z')`, ref); err != nil {
			t.Fatalf("queuing actions/checkout@%s: %v", ref, err)
		}
	}

	g, err := s.FullGraph(FullGraphOptions{Origins: []string{"external"}})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := keysOf(g)
	for _, want := range []string{"actions/checkout@v4", "actions/checkout@v3"} {
		if !keys[want] {
			t.Errorf("%s is external and disappeared when its callers were filtered out", want)
		}
	}
}

// The delta must answer the same question the payload it updates answered.
//
// This is the `scope` leak of 2026-09-10 in its second form: the delta built its options from the
// shared predicate but never applied the node predicates, so an arrival the same predicate
// excludes from the full graph still reached the client and merged into the picture. A reader
// would watch nodes appear that their own filter says are not there.
func TestTheDeltaExcludesWhatTheSamePredicateExcludesFromTheFullGraph(t *testing.T) {
	s := subgraphFixture(t)

	// The cursor has to be taken before the rows it is meant to see, or the watermark sits past
	// every one of them and the delta comes back empty — which reads as a pass.
	gen, err := s.RecordGraphGeneration(GraphRebuild{})
	if err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}

	repoID := func(name string) int64 {
		t.Helper()
		var id int64
		if err := s.DB().QueryRow(
			`SELECT row_id FROM repository WHERE repository_full_name = ?`, name).Scan(&id); err != nil {
			t.Fatalf("finding %s: %v", name, err)
		}
		return id
	}
	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "octo-org/app/.github/workflows/release.yml", Kind: "workflow",
			RepositoryID: repoID("octo-org/app"), Path: ".github/workflows/release.yml"},
		{Key: "octo-org/shared/.github/workflows/release.yml", Kind: "workflow",
			RepositoryID: repoID("octo-org/shared"), Path: ".github/workflows/release.yml"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	delta, err := s.GraphDelta(gen, FullGraphOptions{Origins: []string{OriginSeed}})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if delta.Reset {
		t.Fatal("the delta asked for a reload, so it carried no arrivals to judge")
	}

	arrived := map[string]bool{}
	for _, n := range delta.Nodes {
		arrived[n.Key] = true
	}
	if !arrived["octo-org/app/.github/workflows/release.yml"] {
		t.Error("a seed arrival was dropped, so this proves nothing about what was excluded")
	}
	if arrived["octo-org/shared/.github/workflows/release.yml"] {
		t.Error("the delta carried an upstream arrival that origins=seed excludes from the graph")
	}
}

// A reference style is a property of the edges pointing at a node, exactly as the browser reads
// it, and it has to narrow or the `coverage-gaps` built-in claims "unpinned" while answering
// "everything unanalyzed".
//
// Found by the corpus, not by a unit test: `?flags=unanalyzed&ref=tag` returned **10,305 nodes**,
// identical to `?flags=unanalyzed` alone, because `RefStyle` was accepted and never applied. That
// is the same silently-ignored-predicate defect as `origins`, shipped inside a built-in whose name
// makes a promise the server was not keeping.
func TestTheReferenceStylePredicateNarrowsToHowVersionsAreNamed(t *testing.T) {
	s := subgraphFixture(t)

	tagged, err := s.FullGraph(FullGraphOptions{RefStyle: RefStyleTag})
	if err != nil {
		t.Fatalf("FullGraph(tag): %v", err)
	}
	pinned, err := s.FullGraph(FullGraphOptions{RefStyle: RefStyleSHA})
	if err != nil {
		t.Fatalf("FullGraph(sha): %v", err)
	}

	// The fixture pins nothing by commit: every edge names v1, v3 or v4.
	if !keysOf(tagged)["actions/checkout"] {
		t.Error("a dependency named by tag was dropped by ref=tag")
	}
	if len(pinned.Nodes) != 0 {
		t.Errorf("ref=sha kept %d nodes in a corpus with no commit pins", len(pinned.Nodes))
	}
	if len(tagged.Nodes) == 0 {
		t.Fatal("ref=tag kept nothing, so the comparison proves nothing")
	}
}

// The predicates are additive: asking two questions returns what answers both.
func TestTwoPredicatesNarrowTogether(t *testing.T) {
	g, err := subgraphFixture(t).FullGraph(FullGraphOptions{
		Origins: []string{OriginSeed}, MinReach: 1,
	})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	if len(g.Nodes) != 0 {
		t.Errorf("no seed node has measured reach, so the pair must select none; got %d",
			len(g.Nodes))
	}
}

// The regression guard that matters most: an absent predicate must not become a filter. Every
// caller that asks nothing has to keep getting exactly what it got before.
func TestAnAbsentPredicateFiltersNothing(t *testing.T) {
	s := subgraphFixture(t)
	all, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	empty, err := s.FullGraph(FullGraphOptions{Origins: []string{}, MinReach: 0})
	if err != nil {
		t.Fatalf("FullGraph with empty predicates: %v", err)
	}

	if len(all.Nodes) != len(empty.Nodes) || len(all.Edges) != len(empty.Edges) {
		t.Errorf("an empty predicate changed the graph: %d/%d became %d/%d",
			len(all.Nodes), len(all.Edges), len(empty.Nodes), len(empty.Edges))
	}
	if len(all.Nodes) == 0 {
		t.Fatal("the fixture drew nothing, so this proves nothing")
	}
}
