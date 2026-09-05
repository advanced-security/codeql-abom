package graph

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// fixture builds a small organization by hand so expected reach can be reasoned about exactly:
//
//	app      workflow -> shared reusable workflow -> actions/checkout
//	service  workflow -> shared reusable workflow
//	tool     workflow -> actions/checkout            (direct, no intermediary)
//	lib      composite action greet   -> actions/cache
//	app      workflow -> lib greet
func fixture(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	w := store.NewWriter(s, 0)
	repoIDs := map[string]int64{}
	for _, name := range []string{"octo-org/app", "octo-org/service", "octo-org/tool", "octo-org/lib", "octo-org/shared"} {
		id, err := s.UpsertRepository(name, "")
		if err != nil {
			t.Fatalf("UpsertRepository: %v", err)
		}
		repoIDs[name] = id
	}

	addNode := func(key, kind, repo, container string) {
		t.Helper()
		n := store.Node{
			Key: key, Kind: kind, RepositoryID: repoIDs[repo], ContainerKey: container,
			IsExternal: repo == "",
		}
		// Ingestion records owner and repo for external targets, and advisory matching
		// joins on them, so the fixture has to carry them too.
		if n.IsExternal {
			if parts := strings.SplitN(key, "/", 3); len(parts) >= 2 {
				n.Owner, n.Repo = parts[0], parts[1]
			}
		}
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", key, err)
		}
	}
	// Edges carry raw extraction only; verdicts arrive later as findings keyed by rule id.
	srcRepo := map[string]string{
		"app-step": "octo-org/app", "app-step2": "octo-org/app",
		"service-step": "octo-org/service", "tool-step": "octo-org/tool",
		"shared-step": "octo-org/shared", "lib-step": "octo-org/lib",
	}
	line := 0
	addEdge := func(key, src, dst string) {
		t.Helper()
		line++
		if err := w.AddEdge(store.Edge{
			Key: key, SrcKey: src, DstKey: dst, Kind: "step-uses-action",
			FilePath: ".github/workflows/ci.yml", StartLine: line,
			RepositoryID: repoIDs[srcRepo[src]],
		}); err != nil {
			t.Fatalf("AddEdge(%s): %v", key, err)
		}
	}

	// Containers must exist before the steps that reference them.
	addNode("octo-org/shared/.github/workflows/build.yml", "workflow", "octo-org/shared", "")
	addNode("octo-org/lib/greet", "composite_action", "octo-org/lib", "")
	addNode("octo-org/app/.github/workflows/ci.yml", "workflow", "octo-org/app", "")
	addNode("octo-org/service/.github/workflows/ci.yml", "workflow", "octo-org/service", "")
	addNode("octo-org/tool/.github/workflows/ci.yml", "workflow", "octo-org/tool", "")
	addNode("actions/checkout", "action", "", "")
	addNode("actions/cache", "action", "", "")

	addNode("app-step", "step", "octo-org/app", "octo-org/app/.github/workflows/ci.yml")
	addNode("app-step2", "step", "octo-org/app", "octo-org/app/.github/workflows/ci.yml")
	addNode("service-step", "step", "octo-org/service", "octo-org/service/.github/workflows/ci.yml")
	addNode("tool-step", "step", "octo-org/tool", "octo-org/tool/.github/workflows/ci.yml")
	addNode("shared-step", "step", "octo-org/shared", "octo-org/shared/.github/workflows/build.yml")
	addNode("lib-step", "step", "octo-org/lib", "octo-org/lib/greet")

	addEdge("e1", "app-step", "octo-org/shared/.github/workflows/build.yml")
	addEdge("e2", "service-step", "octo-org/shared/.github/workflows/build.yml")
	addEdge("e3", "shared-step", "actions/checkout")
	addEdge("e4", "tool-step", "actions/checkout")
	addEdge("e5", "lib-step", "actions/cache")
	addEdge("e6", "app-step2", "octo-org/lib/greet")

	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return s
}

func blastRadius(t *testing.T, s *store.Store, key string) (repos, workflows, direct int) {
	t.Helper()
	err := s.DB().QueryRow(`
		SELECT b.dependent_repos, b.dependent_workflows, b.direct_dependents
		FROM abom_blast_radius b JOIN abom_node n ON n.row_id = b.node_row_id
		WHERE n.node_key = ?`, key).Scan(&repos, &workflows, &direct)
	if err != nil {
		t.Fatalf("blast radius for %q: %v", key, err)
	}
	return
}

// upstreamUser adds a third-party repository that was fetched only to resolve dependencies, and
// whose own CI uses the given action, mirroring what expanding an upstream dependency ingests.
func upstreamUser(t *testing.T, s *store.Store, name, target string) {
	t.Helper()
	id, err := s.InsertUpstreamRepository(name)
	if err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}
	w := store.NewWriter(s, 0)
	workflow := name + "/.github/workflows/ci.yml"
	step := name + "-ci-step"
	for _, n := range []store.Node{
		// The target is re-added because a writer resolves edge endpoints from its own cache,
		// which is what ingesting each repository separately does.
		{Key: target, Kind: "action", IsExternal: true},
		{Key: workflow, Kind: "workflow", RepositoryID: id},
		{Key: step, Kind: "step", RepositoryID: id, ContainerKey: workflow},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(store.Edge{
		Key: name + "-ci-edge", SrcKey: step, DstKey: target, Kind: "step-uses-action",
		FilePath: ".github/workflows/ci.yml", StartLine: 99, RepositoryID: id,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

// An upstream repository is fetched to find out what it depends on, not because it is under
// review. Counting its CI as blast radius would inflate the organization's exposure with
// repositories the organization does not own and cannot remediate.
func TestBlastRadiusExcludesUpstreamRepositories(t *testing.T) {
	s := fixture(t)
	measure := func() (repos, workflows int) {
		if err := (&Builder{Store: s}).BuildBlastRadius(); err != nil {
			t.Fatalf("BuildBlastRadius: %v", err)
		}
		repos, workflows, _ = blastRadius(t, s, "actions/checkout")
		return repos, workflows
	}
	wantRepos, wantWorkflows := measure()

	upstreamUser(t, s, "third-party/tool", "actions/checkout")

	gotRepos, gotWorkflows := measure()
	if gotRepos != wantRepos {
		t.Errorf("dependent_repos = %d after adding an upstream user, want %d", gotRepos, wantRepos)
	}
	if gotWorkflows != wantWorkflows {
		t.Errorf("dependent_workflows = %d, want %d: an upstream repository's own CI is not a "+
			"workflow the organization can act on", gotWorkflows, wantWorkflows)
	}
}

// An action whose own CI uses itself must not be counted as depending on itself.
func TestBlastRadiusExcludesSelfReferenceFromUpstreamCI(t *testing.T) {
	s := fixture(t)
	upstreamUser(t, s, "actions/checkout-repo", "actions/checkout")

	if err := (&Builder{Store: s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	repos, _, _ := blastRadius(t, s, "actions/checkout")
	if repos != 4 {
		t.Errorf("dependent_repos = %d, want the 4 organization repositories only", repos)
	}
}

// Upstream repositories still have to contribute edges, or a composite action's own steps would
// never be attributed to the repositories that call it.
func TestUpstreamRepositoriesStillContributeTransitiveEdges(t *testing.T) {
	s := fixture(t)

	// third-party/widget is a composite action app depends on, and it uses actions/cache.
	id, err := s.InsertUpstreamRepository("third-party/widget")
	if err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}
	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "actions/cache", Kind: "action", IsExternal: true},
		{Key: "app-step", Kind: "step", RepositoryID: repoID(t, s, "octo-org/app"),
			ContainerKey: "octo-org/app/.github/workflows/ci.yml"},
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow",
			RepositoryID: repoID(t, s, "octo-org/app")},
		{Key: "third-party/widget", Kind: "composite_action", RepositoryID: id, IsExternal: true},
		{Key: "widget-step", Kind: "step", RepositoryID: id, ContainerKey: "third-party/widget"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(store.Edge{
		Key: "widget-uses-cache", SrcKey: "widget-step", DstKey: "actions/cache",
		Kind: "composite-step-uses-action", FilePath: "action.yml", StartLine: 5,
		RepositoryID: id,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.AddEdge(store.Edge{
		Key: "app-uses-widget", SrcKey: "app-step", DstKey: "third-party/widget",
		Kind: "step-uses-action", FilePath: ".github/workflows/ci.yml", StartLine: 50,
		RepositoryID: repoID(t, s, "octo-org/app"),
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if err := (&Builder{Store: s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	// lib reaches cache directly; app now reaches it through the upstream composite action.
	repos, _, _ := blastRadius(t, s, "actions/cache")
	if repos != 2 {
		t.Errorf("dependent_repos = %d, want lib and app", repos)
	}
}

func repoID(t *testing.T, s *store.Store, name string) int64 {
	t.Helper()
	var id int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = ?`, name).Scan(&id); err != nil {
		t.Fatalf("resolving %s: %v", name, err)
	}
	return id
}

// A repository under review can also be an upstream dependency of another. Observed in real
// data: an organization analyzing actions/cache also depends on it. It must stay under review,
// or it disappears from its own report.
func TestSeedRepositoryIsNotDemotedToUpstream(t *testing.T) {
	s := fixture(t)
	if _, err := s.InsertUpstreamRepository("octo-org/shared"); err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}

	var origin string
	if err := s.DB().QueryRow(
		`SELECT origin FROM repository WHERE repository_full_name = 'octo-org/shared'`).
		Scan(&origin); err != nil {
		t.Fatalf("reading origin: %v", err)
	}
	if origin != store.OriginSeed {
		t.Errorf("origin = %q, want it to remain seed", origin)
	}
}

// "What is the blast radius of this action?" actions/checkout is used directly by tool and
// transitively by app and service through the shared reusable workflow.
func TestBlastRadiusIncludesTransitiveDependents(t *testing.T) {
	s := fixture(t)
	b := &Builder{Store: s}
	if err := b.BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	repos, _, direct := blastRadius(t, s, "actions/checkout")
	if repos != 4 {
		t.Errorf("dependent_repos = %d, want 4 (tool direct; shared, app, service transitive)", repos)
	}
	if direct != 2 {
		t.Errorf("direct_dependents = %d, want 2", direct)
	}
}

func TestBlastRadiusOfSharedWorkflow(t *testing.T) {
	s := fixture(t)
	if err := (&Builder{Store: s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	repos, _, direct := blastRadius(t, s, "octo-org/shared/.github/workflows/build.yml")
	if repos != 2 {
		t.Errorf("dependent_repos = %d, want 2 (app and service)", repos)
	}
	if direct != 2 {
		t.Errorf("direct_dependents = %d, want 2", direct)
	}
}

// A composite action's own dependencies must inherit its callers.
func TestBlastRadiusTraversesCompositeAction(t *testing.T) {
	s := fixture(t)
	if err := (&Builder{Store: s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	repos, _, _ := blastRadius(t, s, "actions/cache")
	if repos != 2 {
		t.Errorf("dependent_repos = %d, want 2 (lib itself and app through greet)", repos)
	}
}

// Mutually-referencing reusable workflows are legal and must not hang the traversal.
func TestTraversalTerminatesOnCycles(t *testing.T) {
	s := fixture(t)
	w := store.NewWriter(s, 0)
	// A fresh writer starts with an empty id cache, so both endpoints must be re-declared.
	for _, n := range []store.Node{
		{Key: "shared-step", Kind: "step"},
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	if err := w.AddEdge(store.Edge{
		Key: "cycle", SrcKey: "shared-step",
		DstKey: "octo-org/app/.github/workflows/ci.yml", Kind: "job-uses-reusable-workflow",
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- (&Builder{Store: s}).BuildBlastRadius() }()
	if err := <-done; err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}
}

func TestMaxDepthBoundsTraversal(t *testing.T) {
	s := fixture(t)
	if err := (&Builder{Store: s, MaxDepth: 1}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	repos, _, _ := blastRadius(t, s, "actions/checkout")
	if repos != 2 {
		t.Errorf("dependent_repos at depth 1 = %d, want 2 (direct callers only)", repos)
	}
}

func TestRebuildIsIdempotent(t *testing.T) {
	s := fixture(t)
	b := &Builder{Store: s}
	for i := 0; i < 2; i++ {
		if err := b.BuildBlastRadius(); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	var rows int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_blast_radius`).Scan(&rows)
	if rows != 4 {
		t.Errorf("got %d rows after two builds, want 4 referenceable nodes", rows)
	}
}

// "Which repositories would a version bump affect?"
func TestDependentsListsAffectedRepositories(t *testing.T) {
	s := fixture(t)
	repos, err := (&Builder{Store: s}).Dependents("actions/checkout")
	if err != nil {
		t.Fatalf("Dependents: %v", err)
	}
	want := map[string]bool{
		"octo-org/tool": true, "octo-org/shared": true,
		"octo-org/app": true, "octo-org/service": true,
	}
	if len(repos) != len(want) {
		t.Fatalf("got %v, want %d repositories", repos, len(want))
	}
	for _, r := range repos {
		if !want[r] {
			t.Errorf("unexpected repository %q", r)
		}
	}
}

func TestDependentsOfUnknownNode(t *testing.T) {
	s := fixture(t)
	if _, err := (&Builder{Store: s}).Dependents("nope/nothing"); err == nil {
		t.Error("expected an error for an unknown node")
	}
}

// seedFindings records results as a CodeQL query would, keyed by the rule that produced them.
// ABOM never decides what "unpinned" means; it counts findings for a rule id.
func seedFindings(t *testing.T, s *store.Store, ruleID string, repos ...string) {
	t.Helper()
	var findings []store.Finding
	for i, repo := range repos {
		id, err := s.UpsertRepository(repo, "")
		if err != nil {
			t.Fatalf("UpsertRepository: %v", err)
		}
		findings = append(findings, store.Finding{
			RuleID: ruleID, RepositoryID: id,
			FilePath: ".github/workflows/ci.yml", StartLine: i + 1,
			Message: "unpinned", Severity: "warning", SecuritySeverity: 5.0,
		})
	}
	if _, err := s.AddFindings(findings); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
}

func TestBuildAll(t *testing.T) {
	s := fixture(t)
	seedFindings(t, s, "actions/unpinned-tag", "octo-org/app")

	if err := (&Builder{Store: s}).BuildAll(); err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	var radius int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_blast_radius`).Scan(&radius)
	if radius == 0 {
		t.Error("BuildAll produced no blast radius rows")
	}
}

// A finding arrives keyed by file position, and nothing built on the graph can see it until it
// is attributed to a node. Ingesting without attributing loses the result silently.
func TestARunLeavesNoFindingUnplaced(t *testing.T) {
	s := fixture(t)
	seedFindings(t, s, "actions/unpinned-tag", "octo-org/app")

	if err := (&Builder{Store: s}).BuildAll(); err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	var unplaced int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_finding f
		WHERE NOT EXISTS (
		        SELECT 1 FROM abom_finding_node fn WHERE fn.finding_row_id = f.row_id)`).
		Scan(&unplaced); err != nil {
		t.Fatalf("counting unplaced findings: %v", err)
	}
	if unplaced != 0 {
		t.Errorf("%d findings were left unattributed to any node", unplaced)
	}
}

// Exposure is recomputed from scratch, so a run that expands the graph without rebuilding it
// keeps answering remediation questions from the graph as it was before.
func TestBuildAllRefreshesExposure(t *testing.T) {
	s := fixture(t)
	affects(t, s, "GHSA-refresh", "actions/checkout", "")

	if err := (&Builder{Store: s}).BuildAll(); err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	if _, _, ok := exposureOfRepo(t, s, "octo-org/tool", "GHSA-refresh"); !ok {
		t.Error("BuildAll left exposure unbuilt")
	}
}
