package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// A workflow calling a reusable workflow in its own repository is a refactor away from being
// inlined. The same call across two repositories of one account is coordination inside a team.
// The same call to another account is a dependency on a third party.
func TestAReferenceInsideOneRepositoryIsSameRepository(t *testing.T) {
	s := boundaryFixture(t)
	classify(t, s)

	if got := boundaryOf(t, s, "own"); got != BoundarySameRepository {
		t.Errorf("boundary = %q, want %q", got, BoundarySameRepository)
	}
}

func TestAReferenceToASiblingUnderOneAccountIsSameOwner(t *testing.T) {
	s := boundaryFixture(t)
	classify(t, s)

	if got := boundaryOf(t, s, "sibling"); got != BoundarySameOwner {
		t.Errorf("boundary = %q, want %q", got, BoundarySameOwner)
	}
}

func TestAReferenceToAnotherAccountIsCrossOwner(t *testing.T) {
	s := boundaryFixture(t)
	classify(t, s)

	if got := boundaryOf(t, s, "stranger"); got != BoundaryCrossOwner {
		t.Errorf("boundary = %q, want %q", got, BoundaryCrossOwner)
	}
}

// GitHub treats account names case-insensitively, so a differently-cased sibling is still a
// sibling and not a third party.
func TestTheBoundaryIgnoresCase(t *testing.T) {
	s := boundaryFixture(t)
	classify(t, s)

	if got := boundaryOf(t, s, "cased"); got != BoundarySameOwner {
		t.Errorf("boundary = %q, want %q: OCTO-ORG and octo-org are one account", got,
			BoundarySameOwner)
	}
}

// An edge whose target names no repository has an *unknown* boundary. Calling it cross-owner
// would report a third-party dependency nobody observed, which is an absent measurement drawn as
// a measurement — the mistake this repository already has three scars from.
func TestAnUnresolvableEndpointIsUnclassifiedRatherThanCrossOwner(t *testing.T) {
	s := boundaryFixture(t)
	classify(t, s)

	if got := boundaryOf(t, s, "nowhere"); got != "" {
		t.Errorf("boundary = %q, want it unset: the target names no repository", got)
	}
}

// Ordering is not use. The boundary is a property of the edge either way, but a `needs:` edge
// must not become a dependency by acquiring one.
func TestOrderingEdgesAreClassifiedWithoutBecomingDependencies(t *testing.T) {
	s := boundaryFixture(t)
	classify(t, s)

	if got := boundaryOf(t, s, "ordering"); got == "" {
		t.Error("an ordering edge still crosses a boundary and can say which")
	}
	var n int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_edge_live e WHERE ` + DependencyEdgeFilter + `
		  AND e.edge_key = 'ordering'`).Scan(&n); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if n != 0 {
		t.Error("an ordering edge became a dependency")
	}
}

// Settling to zero is what lets this run after every ingest without a second mechanism deciding
// when it is due.
func TestClassifyingTwiceChangesNothing(t *testing.T) {
	s := boundaryFixture(t)
	first := classify(t, s)
	if first == 0 {
		t.Fatal("the first pass classified nothing, so the second proves nothing")
	}

	if again := classify(t, s); again != 0 {
		t.Errorf("the second pass classified %d edges, want none", again)
	}
}

// Re-analysis can move an edge to a different target — that is how correcting the parser
// corrects the graph. The classification only fills what is unset, so an edge that keeps the
// class of a target it no longer points at is a stale verdict that no later pass revisits.
func TestARepointedEdgeIsClassifiedAgain(t *testing.T) {
	s := boundaryFixture(t)
	classify(t, s)
	if got := boundaryOf(t, s, "stranger"); got != BoundaryCrossOwner {
		t.Fatalf("boundary = %q, want %q before the target moves", got, BoundaryCrossOwner)
	}

	w := NewWriter(s, 0)
	// A re-ingest names its endpoints again, which is what gives this writer their row ids.
	for _, n := range []Node{
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow"},
		{Key: "octo-org/shared", Kind: "action", Owner: "octo-org", Repo: "shared",
			IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	if err := w.AddEdge(Edge{
		Key: "stranger", SrcKey: "octo-org/app/.github/workflows/ci.yml",
		DstKey: "octo-org/shared", Kind: "step-uses-action",
		FilePath: ".github/workflows/ci.yml", StartLine: 1,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	classify(t, s)
	if got := boundaryOf(t, s, "stranger"); got != BoundarySameOwner {
		t.Errorf("boundary = %q, want %q: the edge now points at octo-org/shared", got,
			BoundarySameOwner)
	}
}

// A node upsert never revises repository_row_id, so a repository first seen as somebody else's
// dependency has none. Reading the target from the node's own owner and repo is what makes an
// external action classifiable at all.
func TestAnExternalTargetIsClassifiedFromItsOwnName(t *testing.T) {
	s := boundaryFixture(t)
	classify(t, s)

	if got := boundaryOf(t, s, "stranger"); got == "" {
		t.Error("actions/checkout has no repository row and must still classify")
	}
}

// A stored class nobody can read is a column, not a capability. The Explorer draws from
// FullGraph, so the boundary has to arrive there or phase 4 has not happened.
func TestTheGraphCarriesTheBoundaryToTheInterface(t *testing.T) {
	s := boundaryFixture(t)
	classify(t, s)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	// Targets are matched by prefix because a dependency is split per reference into `name@ref`
	// before it reaches the interface.
	want := map[string]string{
		"octo-org/shared":  BoundarySameOwner,
		"actions/checkout": BoundaryCrossOwner,
	}
	seen := map[string]bool{}
	for _, e := range dependencies(g.Edges) {
		for target, w := range want {
			if !strings.HasPrefix(e.Target, target) {
				continue
			}
			seen[target] = true
			if e.Boundary != w {
				t.Errorf("edge to %s has boundary %q, want %q", e.Target, e.Boundary, w)
			}
		}
	}
	for target := range want {
		if !seen[target] {
			t.Errorf("no edge to %s reached the graph at all", target)
		}
	}
}

// Filtering to one class is how an operator asks "what do we depend on that we do not own",
// which is the question the Explorer exists to answer.
func TestTheGraphCanBeLimitedToOneBoundary(t *testing.T) {
	s := boundaryFixture(t)
	classify(t, s)

	g, err := s.FullGraph(FullGraphOptions{Boundary: BoundaryCrossOwner})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	kept := dependencies(g.Edges)
	for _, e := range kept {
		if e.Boundary != BoundaryCrossOwner {
			t.Errorf("edge %s -> %s has boundary %q, want only %q",
				e.Source, e.Target, e.Boundary, BoundaryCrossOwner)
		}
	}
	if len(kept) == 0 {
		t.Error("filtering to cross-owner removed every dependency, so it proves nothing")
	}
}

// An unclassified edge must not vanish from an unfiltered view. Absent is not a class, and
// dropping it would make the graph disagree with itself depending on how it was asked.
func TestAnUnclassifiedEdgeStillAppearsUnfiltered(t *testing.T) {
	s := boundaryFixture(t)
	classify(t, s)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	for _, e := range dependencies(g.Edges) {
		if strings.HasPrefix(e.Target, "local-thing") {
			if e.Boundary != "" {
				t.Errorf("boundary = %q, want it empty", e.Boundary)
			}
			return
		}
	}
	t.Error("the edge with no classifiable target was dropped from the unfiltered graph")
}

// dependencies drops the containment edges the interface synthesizes to hang repositories off
// accounts and members off repositories. They are drawing aids, not observed references, so they
// have no boundary to carry.
func dependencies(edges []GraphEdge) []GraphEdge {
	out := []GraphEdge{}
	for _, e := range edges {
		if e.Kind == "repo-contains" || e.Kind == "org-contains" {
			continue
		}
		out = append(out, e)
	}
	return out
}

func classify(t *testing.T, s *Store) int {
	t.Helper()
	n, err := s.ClassifyEdgeBoundaries()
	if err != nil {
		t.Fatalf("ClassifyEdgeBoundaries: %v", err)
	}
	return n
}

func boundaryOf(t *testing.T, s *Store, edgeKey string) string {
	t.Helper()
	var b sql.NullString
	if err := s.DB().QueryRow(
		`SELECT boundary FROM abom_edge WHERE edge_key = ?`, edgeKey).Scan(&b); err != nil {
		t.Fatalf("reading boundary of %s: %v", edgeKey, err)
	}
	return b.String
}

// boundaryFixture holds one edge of every shape the classification has to tell apart.
func boundaryFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	app, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	w := NewWriter(s, 0)
	add := adder(t, w)
	add(Node{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: app})
	add(Node{Key: "octo-org/app/.github/workflows/build.yml", Kind: "reusable_workflow",
		Owner: "octo-org", Repo: "app", IsExternal: true})
	add(Node{Key: "octo-org/shared", Kind: "action", Owner: "octo-org", Repo: "shared",
		IsExternal: true})
	add(Node{Key: "OCTO-ORG/cased", Kind: "action", Owner: "OCTO-ORG", Repo: "cased",
		IsExternal: true})
	add(Node{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
		IsExternal: true})
	// A target that names no repository at all, which a local `uses: ./thing` produces.
	add(Node{Key: "local-thing", Kind: "composite_action"})
	add(Node{Key: "ordered/after", Kind: "job", Owner: "ordered", Repo: "after", IsExternal: true})

	src := "octo-org/app/.github/workflows/ci.yml"
	for _, e := range []Edge{
		{Key: "own", SrcKey: src, DstKey: "octo-org/app/.github/workflows/build.yml",
			Kind: "job-uses-reusable-workflow", RepositoryID: app},
		{Key: "sibling", SrcKey: src, DstKey: "octo-org/shared",
			Kind: "step-uses-action", RepositoryID: app},
		{Key: "cased", SrcKey: src, DstKey: "OCTO-ORG/cased",
			Kind: "step-uses-action", RepositoryID: app},
		{Key: "stranger", SrcKey: src, DstKey: "actions/checkout",
			Kind: "step-uses-action", RepositoryID: app},
		{Key: "nowhere", SrcKey: src, DstKey: "local-thing",
			Kind: "step-uses-action", RepositoryID: app},
		{Key: "ordering", SrcKey: src, DstKey: "ordered/after",
			Kind: EdgeKindJobNeedsJob, RepositoryID: app},
	} {
		e.FilePath = ".github/workflows/ci.yml"
		e.StartLine = 1
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return s
}
