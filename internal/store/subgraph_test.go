package store

import (
	"path/filepath"
	"testing"
)

// subgraphFixture mirrors the shape found in real data: a repository reaching an action
// directly, and another reaching the same action only through a reusable workflow owned by an
// upstream repository.
//
//	octo-org/tool   ci.yml ──────────────────────────────▶ actions/checkout
//	octo-org/app    ci.yml ──▶ octo-org/shared build.yml ─▶ actions/checkout
func subgraphFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	seed := func(name string) int64 {
		id, err := s.UpsertRepository(name, "")
		if err != nil {
			t.Fatalf("UpsertRepository: %v", err)
		}
		return id
	}
	appID, toolID := seed("octo-org/app"), seed("octo-org/tool")
	sharedID, err := s.InsertUpstreamRepository("octo-org/shared")
	if err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}

	w := NewWriter(s, 0)
	add := func(n Node) {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	add(Node{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
		IsExternal: true})
	add(Node{Key: "octo-org/shared/.github/workflows/build.yml", Kind: "reusable_workflow",
		Owner: "octo-org", Repo: "shared", Path: ".github/workflows/build.yml",
		RepositoryID: sharedID, IsExternal: true})

	for _, r := range []struct {
		name string
		id   int64
	}{{"octo-org/app", appID}, {"octo-org/tool", toolID}, {"octo-org/shared", sharedID}} {
		add(Node{Key: r.name + "/.github/workflows/ci.yml", Kind: "workflow",
			RepositoryID: r.id, Path: ".github/workflows/ci.yml"})
	}

	// Two steps in one workflow so collapsing can be observed merging them.
	steps := []struct {
		key, container string
		repoID         int64
		dst, ref       string
		line           int
	}{
		{"step:tool:0", "octo-org/tool/.github/workflows/ci.yml", toolID, "actions/checkout", "v4", 10},
		{"step:tool:1", "octo-org/tool/.github/workflows/ci.yml", toolID, "actions/checkout", "v3", 20},
		{"step:shared:0", "octo-org/shared/.github/workflows/ci.yml", sharedID, "actions/checkout", "v4", 30},
		{"step:app:0", "octo-org/app/.github/workflows/ci.yml", appID, "octo-org/shared/.github/workflows/build.yml", "v1", 40},
	}
	for _, st := range steps {
		add(Node{Key: st.key, Kind: "step", RepositoryID: st.repoID,
			Path: ".github/workflows/ci.yml", ContainerKey: st.container})
	}
	// The upstream reusable workflow's own step, which is what makes app transitively reach it.
	add(Node{Key: "step:shared-build:0", Kind: "step", RepositoryID: sharedID,
		Path:         ".github/workflows/build.yml",
		ContainerKey: "octo-org/shared/.github/workflows/build.yml"})

	edges := []Edge{
		{Key: "e-tool-0", SrcKey: "step:tool:0", DstKey: "actions/checkout",
			Kind: "step-uses-action", RefValue: "v4", RepositoryID: toolID,
			FilePath: ".github/workflows/ci.yml", StartLine: 10},
		{Key: "e-tool-1", SrcKey: "step:tool:1", DstKey: "actions/checkout",
			Kind: "step-uses-action", RefValue: "v3", RepositoryID: toolID,
			FilePath: ".github/workflows/ci.yml", StartLine: 20},
		{Key: "e-shared-ci", SrcKey: "step:shared:0", DstKey: "actions/checkout",
			Kind: "step-uses-action", RefValue: "v4", RepositoryID: sharedID,
			FilePath: ".github/workflows/ci.yml", StartLine: 30},
		{Key: "e-shared-build", SrcKey: "step:shared-build:0", DstKey: "actions/checkout",
			Kind: "step-uses-action", RefValue: "v4", RepositoryID: sharedID,
			FilePath: ".github/workflows/build.yml", StartLine: 5},
		{Key: "e-app", SrcKey: "step:app:0", DstKey: "octo-org/shared/.github/workflows/build.yml",
			Kind: "job-uses-reusable-workflow", RefValue: "v1", RepositoryID: appID,
			FilePath: ".github/workflows/ci.yml", StartLine: 40},
	}
	for _, e := range edges {
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return s
}

func nodeKeys(g Subgraph) map[string]SubgraphNode {
	out := make(map[string]SubgraphNode, len(g.Nodes))
	for _, n := range g.Nodes {
		out[n.Key] = n
	}
	return out
}

func edgeBetween(t *testing.T, g Subgraph, src, dst string) SubgraphEdge {
	t.Helper()
	for _, e := range g.Edges {
		if e.Source == src && e.Target == dst {
			return e
		}
	}
	t.Fatalf("no edge %s -> %s in %+v", src, dst, g.Edges)
	return SubgraphEdge{}
}

// A step is an implementation detail; what a reader recognizes is the workflow. Collapsing is
// also what keeps the rendered graph inside its budget.
func TestSubgraphCollapsesStepsIntoTheirWorkflow(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{Focus: "actions/checkout", Hops: 1})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	for _, n := range g.Nodes {
		if n.Kind == "step" || n.Kind == "job" {
			t.Errorf("%s survived collapsing", n.Key)
		}
	}
	if _, ok := nodeKeys(g)["octo-org/tool/.github/workflows/ci.yml"]; !ok {
		t.Error("the collapsed step's workflow is missing")
	}
}

// Two steps in one workflow using the same action are one relationship, and the count is what
// tells a reader how heavily it is used.
func TestSubgraphMergesParallelEdgesAndKeepsRefs(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{Focus: "actions/checkout", Hops: 1})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	e := edgeBetween(t, g, "octo-org/tool/.github/workflows/ci.yml", "actions/checkout")
	if e.Count != 2 {
		t.Errorf("Count = %d, want the two steps merged", e.Count)
	}
	// References are evidence and must survive verbatim, both of them.
	refs := map[string]bool{}
	for _, r := range e.Refs {
		refs[r] = true
	}
	if !refs["v4"] || !refs["v3"] {
		t.Errorf("Refs = %v, want both observed references", e.Refs)
	}
}

// The cross-repository route is the thing no single-repository analysis can see, so it has to
// survive into the view.
func TestSubgraphReachesTransitiveDependentsAcrossRepositories(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{Focus: "actions/checkout", Hops: 2})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	nodes := nodeKeys(g)

	shared, ok := nodes["octo-org/shared/.github/workflows/build.yml"]
	if !ok {
		t.Fatal("the intermediary reusable workflow is missing")
	}
	if shared.Hops != 1 {
		t.Errorf("intermediary at %d hops, want 1", shared.Hops)
	}
	app, ok := nodes["octo-org/app/.github/workflows/ci.yml"]
	if !ok {
		t.Fatal("the transitively dependent workflow is missing")
	}
	if app.Hops != 2 {
		t.Errorf("transitive dependent at %d hops, want 2", app.Hops)
	}
}

func TestSubgraphStopsAtTheHopBudget(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{Focus: "actions/checkout", Hops: 1})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	if _, ok := nodeKeys(g)["octo-org/app/.github/workflows/ci.yml"]; ok {
		t.Error("a two-hop dependent was included at a one-hop budget")
	}
}

// Seed and upstream must stay distinguishable, or a view implies the organization owns
// repositories it does not.
func TestSubgraphDistinguishesSeedFromUpstream(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{Focus: "actions/checkout", Hops: 2})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	nodes := nodeKeys(g)

	if got := nodes["octo-org/tool/.github/workflows/ci.yml"].Origin; got != OriginSeed {
		t.Errorf("tool origin = %q, want seed", got)
	}
	if got := nodes["octo-org/shared/.github/workflows/build.yml"].Origin; got != OriginUpstream {
		t.Errorf("shared origin = %q, want upstream", got)
	}
}

// The focus is what the reader asked for and stays whatever it is, uncollapsed.
func TestSubgraphKeepsTheFocusAtZeroHops(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{Focus: "actions/checkout", Hops: 2})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	focus, ok := nodeKeys(g)["actions/checkout"]
	if !ok {
		t.Fatal("focus node missing")
	}
	if focus.Hops != 0 {
		t.Errorf("focus at %d hops, want 0", focus.Hops)
	}
	if g.Focus != "actions/checkout" {
		t.Errorf("Focus = %q", g.Focus)
	}
}

// Walking the other way answers "what does this workflow pull in", which is the repository view.
func TestSubgraphWalksDependenciesForward(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{
		Focus: "octo-org/tool/.github/workflows/ci.yml", Hops: 1,
		Direction: DirectionDependencies,
	})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	if _, ok := nodeKeys(g)["actions/checkout"]; !ok {
		t.Error("the action the workflow uses is missing")
	}
	edgeBetween(t, g, "octo-org/tool/.github/workflows/ci.yml", "actions/checkout")
}

// A rendered graph has a budget; exceeding it silently would make the view quietly wrong.
func TestSubgraphReportsTruncation(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{Focus: "actions/checkout", Hops: 3, MaxNodes: 2})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	if len(g.Nodes) > 2 {
		t.Errorf("returned %d nodes, want the budget respected", len(g.Nodes))
	}
	if !g.Truncated {
		t.Error("truncation was not reported")
	}
}

// One hop from a widely used action reaches thousands of workflows, which is past what any
// layout can show. Rolling up to repositories is what makes a hub comprehensible.
func TestSubgraphRepositoryGranularityFoldsWorkflows(t *testing.T) {
	s := subgraphFixture(t)
	addRepoNodes(t, s)

	g, err := s.Subgraph(SubgraphOptions{
		Focus: "actions/checkout", Hops: 1, Granularity: GranularityRepository,
	})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	for _, n := range g.Nodes {
		if n.Kind == "workflow" || n.Kind == "step" {
			t.Errorf("%s survived the repository roll-up", n.Key)
		}
	}
	nodes := nodeKeys(g)
	if _, ok := nodes["repo:octo-org/tool"]; !ok {
		t.Errorf("the depending repository is missing from %v", nodes)
	}
}

// addRepoNodes gives the fixture the repo-kind nodes a repository roll-up folds into.
func addRepoNodes(t *testing.T, s *Store) {
	t.Helper()
	w := NewWriter(s, 0)
	for _, name := range []string{"octo-org/app", "octo-org/tool", "octo-org/shared"} {
		id, err := s.UpsertRepository(name, "")
		if err != nil {
			t.Fatalf("UpsertRepository: %v", err)
		}
		if err := w.AddNode(Node{Key: "repo:" + name, Kind: "repo", RepositoryID: id}); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	deriveContainment(t, s)
}

// Having clicked a node, a reader wants everything touching it, not one side of it. Walking a
// single direction from a reusable workflow hides either who calls it or what it pulls in.
func TestSubgraphBothDirectionsReachesCallersAndCallees(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{
		Focus:     "octo-org/shared/.github/workflows/build.yml",
		Hops:      1,
		Direction: DirectionBoth,
	})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	nodes := nodeKeys(g)
	if _, ok := nodes["octo-org/app/.github/workflows/ci.yml"]; !ok {
		t.Errorf("the caller is missing from %v", nodes)
	}
	if _, ok := nodes["actions/checkout"]; !ok {
		t.Errorf("the callee is missing from %v", nodes)
	}
}

// Edges keep their real orientation under a two-way walk, or the arrows lie about who uses whom.
func TestSubgraphBothDirectionsKeepsEdgeOrientation(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{
		Focus:     "octo-org/shared/.github/workflows/build.yml",
		Hops:      1,
		Direction: DirectionBoth,
	})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	edgeBetween(t, g, "octo-org/app/.github/workflows/ci.yml",
		"octo-org/shared/.github/workflows/build.yml")
	edgeBetween(t, g, "octo-org/shared/.github/workflows/build.yml", "actions/checkout")
}

// A hop count is a budget the reader did not ask for. Left unset the walk runs until the graph
// is exhausted or the node budget stops it, and the node budget is the honest limit.
func TestSubgraphUnboundedHopsWalksUntilTheGraphIsExhausted(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{Focus: "actions/checkout"})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	// app reaches checkout only through the upstream reusable workflow, so it is two hops out.
	nodes := nodeKeys(g)
	app, ok := nodes["octo-org/app/.github/workflows/ci.yml"]
	if !ok {
		t.Fatalf("the two-hop dependent is missing from %v", nodes)
	}
	if app.Hops != 2 {
		t.Errorf("Hops = %d, want 2", app.Hops)
	}
}

// The node budget still applies without a hop limit, and hitting it has to be reported.
func TestSubgraphUnboundedHopsStillReportsTruncation(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{Focus: "actions/checkout", MaxNodes: 2})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	if !g.Truncated {
		t.Error("the node budget was reached but Truncated is false")
	}
}

// Asking a reader to name a focus before anything is drawn asks them to know the answer first.
func TestSubgraphWithoutAFocusCoversEveryRepository(t *testing.T) {
	s := subgraphFixture(t)
	addRepoNodes(t, s)

	g, err := s.Subgraph(SubgraphOptions{Granularity: GranularityRepository})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}

	nodes := nodeKeys(g)
	for _, want := range []string{
		"repo:octo-org/app", "repo:octo-org/tool", "repo:octo-org/shared", "actions/checkout",
	} {
		if _, ok := nodes[want]; !ok {
			t.Errorf("%s is missing from the overview %v", want, nodes)
		}
	}
	edgeBetween(t, g, "repo:octo-org/tool", "actions/checkout")
	edgeBetween(t, g, "repo:octo-org/app", "repo:octo-org/shared")
}

func TestSubgraphWithoutAFocusIsNeverNil(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	g, err := s.Subgraph(SubgraphOptions{})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	if g.Nodes == nil || g.Edges == nil {
		t.Error("an empty corpus produced nil collections")
	}
}

// The whole corpus is what the explorer opens on, so nothing may be dropped from it.
func TestFullGraphCoversEveryRepositoryActionAndWorkflow(t *testing.T) {
	s := subgraphFixture(t)
	addRepoNodes(t, s)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := map[string]GraphNode{}
	for _, n := range g.Nodes {
		keys[n.Key] = n
	}
	for _, want := range []string{
		"repo:octo-org/app", "repo:octo-org/tool", "repo:octo-org/shared",
		"actions/checkout", "octo-org/shared/.github/workflows/build.yml",
		"octo-org/app/.github/workflows/ci.yml",
	} {
		if _, ok := keys[want]; !ok {
			t.Errorf("%s is missing from the full graph", want)
		}
	}
	for _, n := range g.Nodes {
		if n.Kind == "step" || n.Kind == "job" {
			t.Errorf("%s survived the fold", n.Key)
		}
	}
}

// Folding a step away must not fold away the reference it carried.
func TestFullGraphFoldsStepEdgesIntoTheirWorkflow(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	var found *GraphEdge
	for i, e := range g.Edges {
		if e.Source == "octo-org/tool/.github/workflows/ci.yml" && e.Target == "actions/checkout" {
			found = &g.Edges[i]
		}
	}
	if found == nil {
		t.Fatalf("the folded edge is missing from %+v", g.Edges)
	}
	// Two steps in that workflow reference checkout at different refs; both must survive.
	if len(found.Refs) != 2 {
		t.Errorf("Refs = %v, want both v3 and v4", found.Refs)
	}
	if found.Count != 2 {
		t.Errorf("Count = %d, want 2", found.Count)
	}
}

func TestFullGraphIncludesStepsWhenAsked(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.FullGraph(FullGraphOptions{IncludeSteps: true})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	if !g.IncludesSteps {
		t.Error("IncludesSteps is false after asking for steps")
	}

	var steps int
	for _, n := range g.Nodes {
		if n.Kind == "step" {
			steps++
		}
	}
	if steps == 0 {
		t.Error("no step survived a request for steps")
	}
}

// Every verdict in the interface is filtered by rule id, so rule ids have to travel with the
// node. Inferring severity from anything else is how a pin_status column gets reinvented.
func TestFullGraphCarriesRuleIdsForFiltering(t *testing.T) {
	s := subgraphFixture(t)
	seedFindingOnStep(t, s, "step:tool:0", "actions/unpinned-tag")

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	for _, n := range g.Nodes {
		if n.Key != "octo-org/tool/.github/workflows/ci.yml" {
			continue
		}
		if n.Findings != 1 {
			t.Errorf("Findings = %d, want 1 folded up from the step", n.Findings)
		}
		if len(n.Rules) != 1 || n.Rules[0] != "actions/unpinned-tag" {
			t.Errorf("Rules = %v, want [actions/unpinned-tag]", n.Rules)
		}
		return
	}
	t.Error("the workflow holding the finding is missing from the full graph")
}

// A repository node with no edges floats unattached, and isolating it finds nothing. The
// repository holds its workflows, and that containment is a real relationship.
func TestFullGraphConnectsRepositoriesToWhatTheyContain(t *testing.T) {
	s := subgraphFixture(t)
	addRepoNodes(t, s)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	var found bool
	for _, e := range g.Edges {
		if e.Source == "repo:octo-org/tool" &&
			e.Target == "octo-org/tool/.github/workflows/ci.yml" {
			found = true
		}
	}
	if !found {
		t.Errorf("the repository is not connected to its own workflow in %+v", g.Edges)
	}

	// Every repository has to reach something, or it is unreachable in the interface.
	degree := map[string]int{}
	for _, e := range g.Edges {
		degree[e.Source]++
		degree[e.Target]++
	}
	for _, n := range g.Nodes {
		if n.Kind == "repo" && degree[n.Key] == 0 {
			t.Errorf("%s has no edges", n.Key)
		}
	}
}

// Containment stops at what a repository directly holds. Wiring every step to its repository as
// well would add tens of thousands of edges that say nothing the workflow edge does not.
func TestFullGraphDoesNotConnectRepositoriesDirectlyToSteps(t *testing.T) {
	s := subgraphFixture(t)
	addRepoNodes(t, s)

	g, err := s.FullGraph(FullGraphOptions{IncludeSteps: true})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	kind := map[string]string{}
	for _, n := range g.Nodes {
		kind[n.Key] = n.Kind
	}
	for _, e := range g.Edges {
		if kind[e.Source] == "repo" && (kind[e.Target] == "step" || kind[e.Target] == "job") {
			t.Errorf("repository wired straight to %s %s", kind[e.Target], e.Target)
		}
	}
}

func TestFullGraphCollectionsAreNeverNil(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	if g.Nodes == nil || g.Edges == nil {
		t.Error("an empty corpus produced nil collections")
	}
}

func seedFindingOnStep(t *testing.T, s *Store, stepKey, rule string) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO abom_finding (rule_id, repository_row_id, severity, message)
		SELECT ?, n.repository_row_id, 'warning', 'seeded'
		FROM abom_node n WHERE n.node_key = ?`, rule, stepKey); err != nil {
		t.Fatalf("inserting finding: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT (SELECT MAX(row_id) FROM abom_finding),
		       row_id FROM abom_node WHERE node_key = ?`, stepKey); err != nil {
		t.Fatalf("linking finding: %v", err)
	}
}

// A reader picking a focus has to be able to tell a repository from an action.
func TestSearchNodesLabelsEveryFocusableKind(t *testing.T) {
	s := subgraphFixture(t)
	addRepoNodes(t, s)

	kinds := map[string]string{}
	for _, q := range []string{"octo-org", "checkout"} {
		matches, err := s.SearchNodes(q, 50, RepositoryScope{})
		if err != nil {
			t.Fatalf("SearchNodes(%q): %v", q, err)
		}
		for _, m := range matches {
			kinds[m.Key] = m.Kind
			if m.Kind == "" {
				t.Errorf("%s came back without a kind", m.Key)
			}
		}
	}

	if kinds["repo:octo-org/app"] != "repo" {
		t.Errorf("repo:octo-org/app kind = %q, want repo", kinds["repo:octo-org/app"])
	}
	if kinds["actions/checkout"] != "action" {
		t.Errorf("actions/checkout kind = %q, want action", kinds["actions/checkout"])
	}
}

// A step is not somewhere a reader would choose to stand.
func TestSearchNodesOmitsStepsAndJobs(t *testing.T) {
	s := subgraphFixture(t)

	matches, err := s.SearchNodes("step:", 50, RepositoryScope{})
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("steps are offered as a focus: %+v", matches)
	}
}

// An empty query is the opening state of the picker, so it has to return the widest-reaching
// candidates rather than nothing.
func TestSearchNodesWithoutAQueryRanksByReach(t *testing.T) {
	s := subgraphFixture(t)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_blast_radius
			(node_row_id, dependent_repos, dependent_workflows, direct_dependents, max_depth)
		SELECT row_id, 9, 3, 2, 2 FROM abom_node WHERE node_key = 'actions/checkout'`); err != nil {
		t.Fatalf("seeding blast radius: %v", err)
	}

	matches, err := s.SearchNodes("", 10, RepositoryScope{})
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("the picker opens empty")
	}
	if matches[0].Key != "actions/checkout" {
		t.Errorf("first match = %q, want actions/checkout", matches[0].Key)
	}
	if matches[0].DependentRepos != 9 {
		t.Errorf("DependentRepos = %d, want 9", matches[0].DependentRepos)
	}
}

// A nil slice marshals to null, which a client reading a collection then has to special-case.
// An empty neighborhood is an empty list, not an absent one.
func TestSubgraphEmptyCollectionsAreNeverNil(t *testing.T) {
	s := subgraphFixture(t)

	for _, focus := range []string{"nobody/nothing", "actions/checkout"} {
		g, err := s.Subgraph(SubgraphOptions{Focus: focus, Hops: 1})
		if err != nil {
			t.Fatalf("Subgraph(%s): %v", focus, err)
		}
		if g.Nodes == nil {
			t.Errorf("%s: Nodes is nil", focus)
		}
		if g.Edges == nil {
			t.Errorf("%s: Edges is nil", focus)
		}
	}

	// A node nothing depends on is the case that crashed the interface.
	g, err := s.Subgraph(SubgraphOptions{
		Focus: "octo-org/tool/.github/workflows/ci.yml", Hops: 1,
	})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	if g.Nodes == nil || g.Edges == nil {
		t.Error("a leaf focus produced nil collections")
	}
}

func TestSubgraphOfUnknownNodeIsEmpty(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.Subgraph(SubgraphOptions{Focus: "nobody/nothing", Hops: 2})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	if len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Errorf("unknown focus produced %d nodes / %d edges", len(g.Nodes), len(g.Edges))
	}
}

// Blast radius is what sizes a node in the view, so it has to travel with it.
func TestSubgraphCarriesBlastRadius(t *testing.T) {
	s := subgraphFixture(t)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_blast_radius
			(node_row_id, dependent_repos, dependent_workflows, direct_dependents, max_depth)
		SELECT row_id, 7, 3, 2, 2 FROM abom_node WHERE node_key = 'actions/checkout'`); err != nil {
		t.Fatalf("seeding blast radius: %v", err)
	}

	g, err := s.Subgraph(SubgraphOptions{Focus: "actions/checkout", Hops: 1})
	if err != nil {
		t.Fatalf("Subgraph: %v", err)
	}
	if got := nodeKeys(g)["actions/checkout"].DependentRepos; got != 7 {
		t.Errorf("DependentRepos = %d, want 7", got)
	}
}
