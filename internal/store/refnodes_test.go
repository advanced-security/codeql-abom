package store

import "testing"

// The queue analyzes `actions/checkout@v4` and `actions/checkout@v3` separately, so the Explorer
// has to draw them separately: one node per name reports a single state for versions that were
// analyzed independently and need not agree. The split is a view over the graph — `abom_node` is
// a component identity, joined by findings, exposure and blast radius, and must not become a
// version identity.
func TestADependencyUsedAtSeveralRefsIsSeveralNodes(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, 0)
	repoID, err := s.UpsertRepository("octo/app", OriginSeed)
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	for _, n := range []Node{
		{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: repoID},
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
			IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	for i, ref := range []string{"v4", "v3"} {
		if err := w.AddEdge(Edge{
			Key: "e" + ref, SrcKey: "octo/app/.github/workflows/ci.yml", DstKey: "actions/checkout",
			Kind: "step-uses-action", RefValue: ref, FilePath: "ci.yml", StartLine: i + 1,
			RepositoryID: repoID,
		}); err != nil {
			t.Fatalf("AddEdge(%s): %v", ref, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('actions/checkout', 'v4', 'actions', 'checkout', 1, 'done', '2026-01-01T00:00:00Z'),
		       ('actions/checkout', 'v3', 'actions', 'checkout', 1, 'pending', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}

	graph, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	byKey := map[string]GraphNode{}
	for _, n := range graph.Nodes {
		byKey[n.Key] = n
	}
	v4, ok4 := byKey["actions/checkout@v4"]
	v3, ok3 := byKey["actions/checkout@v3"]
	if !ok4 || !ok3 {
		t.Fatalf("want a node per reference, got %v", graphNodeKeys(graph.Nodes))
	}
	if _, collapsed := byKey["actions/checkout"]; collapsed {
		t.Error("the collapsed node is still drawn beside its references, so both are on screen")
	}

	// Each carries its own state, which is the entire point: one is analyzed and one is not.
	if !v4.Analyzed || v4.Discovery != "done" {
		t.Errorf("v4 is %+v, want analyzed", v4)
	}
	if v3.Analyzed || v3.Discovery != "queued" {
		t.Errorf("v3 is %+v, want queued and unanalyzed", v3)
	}
	if v4.Ref != "v4" || v3.Ref != "v3" {
		t.Errorf("refs are %q and %q, want them carried for the label", v4.Ref, v3.Ref)
	}

	// The edges have to follow, or the versions are drawn unattached to what uses them.
	targets := map[string]bool{}
	for _, e := range graph.Edges {
		targets[e.Target] = true
	}
	if !targets["actions/checkout@v4"] || !targets["actions/checkout@v3"] {
		t.Errorf("edge targets are %v, want each reference reached by its own use", targets)
	}
	if targets["actions/checkout"] {
		t.Error("an edge still points at the collapsed node")
	}
}

// A `uses:` with no `@ref` runs the default branch. It keeps the bare name, because inventing a
// reference for it would put a version on screen that the workflow never wrote.
func TestAUseWithNoReferenceKeepsTheBareName(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, 0)
	repoID, err := s.UpsertRepository("octo/app", OriginSeed)
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	for _, n := range []Node{
		{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: repoID},
		{Key: "actions/labeler", Kind: "action", Owner: "actions", Repo: "labeler",
			IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(Edge{
		Key: "e1", SrcKey: "octo/app/.github/workflows/ci.yml", DstKey: "actions/labeler",
		Kind: "step-uses-action", FilePath: "ci.yml", StartLine: 1, RepositoryID: repoID,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	graph, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	for _, n := range graph.Nodes {
		if n.Key == "actions/labeler" {
			if n.Ref != "" {
				t.Errorf("ref = %q, want empty for a use that names none", n.Ref)
			}
			return
		}
	}
	t.Fatalf("actions/labeler is missing, got %v", graphNodeKeys(graph.Nodes))
}

// Splitting a name into versions rewrites every edge that touches it, and an endpoint naming a
// node the graph does not contain is a break nothing can draw.
func TestSplittingByReferenceLeavesNoEdgeWithoutBothEnds(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, 0)
	repoID, err := s.UpsertRepository("octo/app", OriginSeed)
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	for _, n := range []Node{
		{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: repoID},
		{Key: "octo/build", Kind: "action", Owner: "octo", Repo: "build", IsExternal: true},
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
			IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	// octo/build is used at a version, and its own step is an edge out of it.
	if err := w.AddEdge(Edge{
		Key: "use", SrcKey: "octo/app/.github/workflows/ci.yml", DstKey: "octo/build",
		Kind: "step-uses-action", RefValue: "v2", FilePath: "ci.yml", StartLine: 1,
		RepositoryID: repoID,
	}); err != nil {
		t.Fatalf("AddEdge(use): %v", err)
	}
	if err := w.AddEdge(Edge{
		Key: "inner", SrcKey: "octo/build", DstKey: "actions/checkout",
		Kind: "step-uses-action", RefValue: "v4", FilePath: "action.yml", StartLine: 1,
	}); err != nil {
		t.Fatalf("AddEdge(inner): %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('octo/build', 'v2', 'octo', 'build', 1, 'done', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}

	graph, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := map[string]bool{}
	for _, n := range graph.Nodes {
		keys[n.Key] = true
	}
	for _, e := range graph.Edges {
		if !keys[e.Source] {
			t.Errorf("edge %s -> %s starts outside the graph; nodes are %v",
				e.Source, e.Target, graphNodeKeys(graph.Nodes))
		}
		if !keys[e.Target] {
			t.Errorf("edge %s -> %s ends outside the graph; nodes are %v",
				e.Source, e.Target, graphNodeKeys(graph.Nodes))
		}
	}
}

func graphNodeKeys(nodes []GraphNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Key)
	}
	return out
}
