package store

import "testing"

// Asking for one kind of relationship draws that kind. `needs:` is ordering rather than use, so a
// reader looking at what runs after what is asking a different question from a reader looking at
// what a workflow depends on, and the graph has to be able to answer both.
func TestTheGraphDrawsOnlyTheKindsOfRelationshipAskedFor(t *testing.T) {
	s := edgeKindFixture(t)

	ordering, err := s.FullGraph(FullGraphOptions{EdgeKinds: []string{EdgeKindJobNeedsJob}})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	for _, e := range dependencies(ordering.Edges) {
		if e.Kind != EdgeKindJobNeedsJob {
			t.Errorf("a %s edge is drawn under edgeKinds=%s", e.Kind, EdgeKindJobNeedsJob)
		}
	}
	if len(dependencies(ordering.Edges)) != 1 {
		t.Errorf("%d ordering edges, want the one the fixture declares",
			len(dependencies(ordering.Edges)))
	}

	uses, err := s.FullGraph(FullGraphOptions{EdgeKinds: []string{EdgeKindStepUsesAction}})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	if len(dependencies(uses.Edges)) != 1 {
		t.Errorf("%d uses edges, want the one the fixture declares",
			len(dependencies(uses.Edges)))
	}
}

// Containment is a drawing aid rather than a declared relationship: it hangs a workflow off its
// repository so the node is not an island. Filtering it away with the kinds would answer "show me
// the `needs:` edges" with a scatter of unattached nodes.
func TestNarrowingToAKindKeepsTheContainmentThatPlacesTheNodes(t *testing.T) {
	s := edgeKindFixture(t)

	g, err := s.FullGraph(FullGraphOptions{EdgeKinds: []string{EdgeKindJobNeedsJob}})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	contains := 0
	for _, e := range g.Edges {
		if e.Kind == "repo-contains" {
			contains++
		}
	}
	if contains == 0 {
		t.Error("no containment is drawn, so every node stands unattached to its repository")
	}
}

// A kind nothing produces has an empty answer. Returning the whole graph instead is the failure
// the boundary filter was caught in: the unfiltered corpus arriving under a promise it was
// narrowed.
func TestAKindNothingDeclaresDrawsNoRelationships(t *testing.T) {
	s := edgeKindFixture(t)

	g, err := s.FullGraph(FullGraphOptions{EdgeKinds: []string{"pushes-to-branch"}})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	if n := len(dependencies(g.Edges)); n != 0 {
		t.Errorf("%d relationships drawn for a kind nothing declares", n)
	}
}

// edgeKindFixture is one repository declaring one use and one ordering.
func edgeKindFixture(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	repo, err := s.UpsertRepository("octo-org/app", OriginSeed)
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	w := NewWriter(s, 0)
	workflow := "octo-org/app/.github/workflows/ci.yml"
	for _, n := range []Node{
		{Key: "repo:octo-org/app", Kind: "repo", Owner: "octo-org", Repo: "app",
			RepositoryID: repo},
		{Key: workflow, Kind: "workflow", RepositoryID: repo},
		{Key: "job:octo-org/app:ci.yml:build", Kind: "job", RepositoryID: repo},
		{Key: "job:octo-org/app:ci.yml:test", Kind: "job", RepositoryID: repo},
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
			IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	for _, e := range []Edge{
		{Key: "uses", SrcKey: workflow, DstKey: "actions/checkout",
			Kind: EdgeKindStepUsesAction, RefValue: "v4", FilePath: "ci.yml", StartLine: 9,
			RepositoryID: repo},
		{Key: "needs", SrcKey: "job:octo-org/app:ci.yml:test",
			DstKey: "job:octo-org/app:ci.yml:build", Kind: EdgeKindJobNeedsJob,
			FilePath: "ci.yml", StartLine: 20, RepositoryID: repo},
	} {
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return s
}
