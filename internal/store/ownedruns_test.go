package store

import (
	"slices"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/normalize"
)

// ownedRunsFixture is a declared repository holding a composite action and running another
// starting point's, which runs a third action, beside that starting point's own CI, which nothing
// declared runs.
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
	add(Node{Key: "octo/app/lint", Kind: "composite_action", RepositoryID: app,
		Owner: "octo", Repo: "app", Path: "lint/action.yml"})
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

// unitKeys reads, unit by unit, what a scope keeps, by the key each unit names a row by.
func unitKeys(t *testing.T, s *Store, scope string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	actions, err := s.ActionsInventoryPage(InventoryQuery{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actions.Items {
		out["actions"] = append(out["actions"], a.NodeKey)
	}
	workflows, err := s.WorkflowsPage(WorkflowsQuery{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range workflows.Items {
		out["workflows"] = append(out["workflows"], w.NodeKey)
	}
	jobs, err := s.JobsPage(JobsQuery{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs.Items {
		out["jobs"] = append(out["jobs"], j.NodeKey)
	}
	relationships, err := s.RelationshipsPage(RelationshipsQuery{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range relationships.Items {
		out["relationships"] = append(out["relationships"], r.EdgeKey)
	}
	repositories, err := s.RepositoriesPage(RepositoriesQuery{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repositories.Items {
		out["repositories"] = append(out["repositories"], r.FullName)
	}
	findings, err := s.InventoryFindingsPage(FindingReviewQuery{InventoryQuery: InventoryQuery{Scope: scope}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings.Items {
		out["findings"] = append(out["findings"], f.FilePath)
	}
	g, err := s.FullGraph(FullGraphOptions{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		out["explorer"] = append(out["explorer"], n.Key)
	}
	return out
}

// checkUnits holds every unit to what it must list and what it must not.
func checkUnits(t *testing.T, got map[string][]string, in, out map[string][]string, why string) {
	t.Helper()
	for unit, keys := range in {
		for _, k := range keys {
			if !slices.Contains(got[unit], k) {
				t.Errorf("%s: %s is missing", unit, k)
			}
		}
	}
	for unit, keys := range out {
		for _, k := range keys {
			if slices.Contains(got[unit], k) {
				t.Errorf("%s: %s is listed; %s", unit, k, why)
			}
		}
	}
}

// "Yours and what it runs" is what the declared repositories hold and what they run, at the node:
// the repository of something they run also holds its own CI, which nothing declared runs, and
// admitting the repository whole made that CI, and everything it uses, read as yours in every unit.
func TestYoursAndWhatItRunsIsWhatDeclaredRepositoriesHoldAndRunNotAnotherRepositorysCI(t *testing.T) {
	got := unitKeys(t, ownedRunsFixture(t), ScopeSupplyChain)
	checkUnits(t, got, map[string][]string{
		"actions":       {"octo/app/lint", "vendor/tool/setup", "actions/cache"},
		"workflows":     {"octo/app/ci.yml"},
		"jobs":          {"app-build"},
		"relationships": {"app>setup", "setup>cache"},
		"repositories":  {"octo/app", "vendor/tool"},
		"findings":      {"app-step", "setup-step"},
		"explorer": {"octo/app/ci.yml", "octo/app/lint", "vendor/tool/setup", "actions/cache",
			normalize.RepoKey("vendor/tool")},
	}, map[string][]string{
		"actions":       {"third/lint"},
		"workflows":     {"vendor/tool/ci.yml"},
		"jobs":          {"tool-test"},
		"relationships": {"ci>lint"},
		"repositories":  {"third/lint"},
		"findings":      {"tool-ci-step"},
		"explorer":      {"vendor/tool/ci.yml", "third/lint", normalize.RepoKey("third/lint")},
	}, "only another repository's own CI uses it")
}

// "What you own" is what the declared repositories hold and nothing else, in every unit. What they
// run belongs to whoever holds it: listed here, a vendor's composite action read as yours.
func TestWhatYouOwnIsOnlyWhatDeclaredRepositoriesHold(t *testing.T) {
	s := ownedRunsFixture(t)
	got := unitKeys(t, s, ScopeOwned)
	checkUnits(t, got, map[string][]string{
		"actions":       {"octo/app/lint"},
		"workflows":     {"octo/app/ci.yml"},
		"jobs":          {"app-build"},
		"relationships": {"app>setup"},
		"repositories":  {"octo/app"},
		"findings":      {"app-step"},
		"explorer":      {"octo/app/ci.yml", "octo/app/lint", normalize.RepoKey("octo/app")},
	}, map[string][]string{
		"actions":       {"vendor/tool/setup", "actions/cache", "third/lint"},
		"workflows":     {"vendor/tool/ci.yml"},
		"jobs":          {"tool-test"},
		"relationships": {"setup>cache", "ci>lint"},
		"repositories":  {"vendor/tool", "third/lint"},
		"findings":      {"setup-step", "tool-ci-step"},
		"explorer": {"vendor/tool/setup", "actions/cache", "third/lint", "vendor/tool/ci.yml",
			normalize.RepoKey("vendor/tool"), normalize.RepoKey("third/lint")},
	}, "the declared repositories run it and do not hold it")

	// A repository's own findings page asks the same question of the repository.
	vendor, err := s.InventoryFindingsPage(FindingReviewQuery{AssetKind: "repository", AssetKey: "vendor/tool",
		InventoryQuery: InventoryQuery{Scope: ScopeOwned}})
	if err != nil {
		t.Fatal(err)
	}
	if vendor.Total != 0 {
		t.Errorf("vendor/tool's findings page lists %d findings under what you own", vendor.Total)
	}
}
