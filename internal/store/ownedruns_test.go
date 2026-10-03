package store

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/normalize"
)

// ownedRunsFixture is a declared repository running another starting point's composite action,
// which runs a third action, beside that starting point's own CI, which nothing declared runs.
func ownedRunsFixture(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	vendor, err := s.UpsertRepository("vendor/tool", "")
	if err != nil {
		t.Fatal(err)
	}
	lint, err := s.UpsertRepository("third/lint", "")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, 0)
	add := adder(t, w)
	flush := func() {
		t.Helper()
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	for name, id := range map[string]int64{"octo/app": app, "vendor/tool": vendor, "third/lint": lint} {
		add(Node{Key: normalize.RepoKey(name), Kind: "repo", RepositoryID: id})
	}
	add(Node{Key: "octo/app/ci.yml", Kind: "workflow", RepositoryID: app})
	add(Node{Key: "vendor/tool/ci.yml", Kind: "workflow", RepositoryID: vendor})
	add(Node{Key: "vendor/tool/setup", Kind: "composite_action", RepositoryID: vendor,
		Owner: "vendor", Repo: "tool", Path: "setup/action.yml"})
	add(Node{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache", IsExternal: true})
	add(Node{Key: "third/lint", Kind: "action", Owner: "third", Repo: "lint", IsExternal: true})
	flush()
	add(Node{Key: "app-build", Kind: "job", RepositoryID: app, ContainerKey: "octo/app/ci.yml", JobID: "build"})
	add(Node{Key: "tool-test", Kind: "job", RepositoryID: vendor, ContainerKey: "vendor/tool/ci.yml", JobID: "test"})
	add(Node{Key: "app-step", Kind: "step", RepositoryID: app, ContainerKey: "octo/app/ci.yml", JobID: "build"})
	add(Node{Key: "setup-step", Kind: "step", RepositoryID: vendor, ContainerKey: "vendor/tool/setup"})
	add(Node{Key: "tool-ci-step", Kind: "step", RepositoryID: vendor, ContainerKey: "vendor/tool/ci.yml", JobID: "test"})
	flush()
	for _, e := range []Edge{
		{Key: "app>setup", SrcKey: "app-step", DstKey: "vendor/tool/setup", Kind: EdgeKindStepUsesAction,
			RefValue: "v1", RepositoryID: app, FilePath: "ci.yml", StartLine: 1},
		{Key: "setup>cache", SrcKey: "setup-step", DstKey: "actions/cache", Kind: EdgeKindCompositeStepUsesAction,
			RefValue: "v4", RepositoryID: vendor, FilePath: "setup/action.yml", StartLine: 2},
		{Key: "ci>lint", SrcKey: "tool-ci-step", DstKey: "third/lint", Kind: EdgeKindStepUsesAction,
			RefValue: "v2", RepositoryID: vendor, FilePath: "ci.yml", StartLine: 3},
	} {
		if err := w.AddEdge(e); err != nil {
			t.Fatal(err)
		}
	}
	flush()
	// What the reach walk writes: app runs setup by naming it and cache through it.
	for key, hops := range map[string]int{"vendor/tool/setup": 1, "actions/cache": 2} {
		if _, err := s.DB().Exec(`
			INSERT INTO abom_node_reach (node_row_id, repository_row_id, hops, steps, jobs, workflows)
			SELECT row_id, ?, ?, 1, 1, 1 FROM abom_node WHERE node_key = ?`, app, hops, key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_blast_radius (node_row_id, dependent_repos, dependent_steps, dependent_jobs,
			dependent_workflows, max_depth, refs_in_use)
		SELECT row_id, CASE WHEN node_key = 'third/lint' THEN 0 ELSE 1 END, 1, 1, 1, 1, 1
		  FROM abom_node WHERE node_key IN ('vendor/tool/setup', 'actions/cache', 'third/lint')`); err != nil {
		t.Fatal(err)
	}
	findingOn(t, s, app, "app-step")
	findingOn(t, s, vendor, "setup-step")
	findingOn(t, s, vendor, "tool-ci-step")
	return s
}

// "What you own" is what the declared repositories hold and what they run, at the node: the
// repository of something they run also holds its own CI, which nothing declared runs, and
// admitting the repository whole made that CI, and everything it uses, read as yours in every unit.
func TestWhatYouOwnIsWhatDeclaredRepositoriesHoldAndRunNotAnotherRepositorysCI(t *testing.T) {
	s := ownedRunsFixture(t)
	check := func(unit string, got []string, in, out []string) {
		t.Helper()
		have := map[string]bool{}
		for _, g := range got {
			have[g] = true
		}
		for _, k := range in {
			if !have[k] {
				t.Errorf("%s: %s is missing; a declared repository holds or runs it", unit, k)
			}
		}
		for _, k := range out {
			if have[k] {
				t.Errorf("%s: %s is listed; only another repository's own CI uses it", unit, k)
			}
		}
	}

	actions, err := s.ActionsInventoryPage(InventoryQuery{Scope: ScopeOwned})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, a := range actions.Items {
		keys = append(keys, a.NodeKey)
	}
	check("actions", keys, []string{"vendor/tool/setup", "actions/cache"}, []string{"third/lint"})

	workflows, err := s.WorkflowsPage(WorkflowsQuery{Scope: ScopeOwned})
	if err != nil {
		t.Fatal(err)
	}
	keys = nil
	for _, w := range workflows.Items {
		keys = append(keys, w.NodeKey)
	}
	check("workflows", keys, []string{"octo/app/ci.yml"}, []string{"vendor/tool/ci.yml"})

	jobs, err := s.JobsPage(JobsQuery{Scope: ScopeOwned})
	if err != nil {
		t.Fatal(err)
	}
	keys = nil
	for _, j := range jobs.Items {
		keys = append(keys, j.NodeKey)
	}
	check("jobs", keys, []string{"app-build"}, []string{"tool-test"})

	relationships, err := s.RelationshipsPage(RelationshipsQuery{Scope: ScopeOwned})
	if err != nil {
		t.Fatal(err)
	}
	keys = nil
	for _, r := range relationships.Items {
		keys = append(keys, r.EdgeKey)
	}
	check("relationships", keys, []string{"app>setup", "setup>cache"}, []string{"ci>lint"})

	repositories, err := s.RepositoriesPage(RepositoriesQuery{Scope: ScopeOwned})
	if err != nil {
		t.Fatal(err)
	}
	keys = nil
	for _, r := range repositories.Items {
		keys = append(keys, r.FullName)
	}
	check("repositories", keys, []string{"octo/app", "vendor/tool"}, []string{"third/lint"})

	findings, err := s.InventoryFindingsPage(FindingReviewQuery{InventoryQuery: InventoryQuery{Scope: ScopeOwned}})
	if err != nil {
		t.Fatal(err)
	}
	keys = nil
	for _, f := range findings.Items {
		keys = append(keys, f.FilePath)
	}
	check("findings", keys, []string{"app-step", "setup-step"}, []string{"tool-ci-step"})

	g, err := s.FullGraph(FullGraphOptions{Scope: ScopeOwned})
	if err != nil {
		t.Fatal(err)
	}
	keys = nil
	for _, n := range g.Nodes {
		keys = append(keys, n.Key)
	}
	check("explorer", keys,
		[]string{"octo/app/ci.yml", "vendor/tool/setup", "actions/cache", normalize.RepoKey("vendor/tool")},
		[]string{"vendor/tool/ci.yml", "third/lint", normalize.RepoKey("third/lint")})
}
