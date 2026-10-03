package graph

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// releaseFixture is the defect per-sighting content exists for: two repositories pin different
// releases of vendor/comp, whose composite step at one location declared actions/cache@v3 at c1
// and actions/setup-go@v6 at c2. One row holds that location, written last by c2.
type releaseFixture struct {
	s    *store.Store
	w    *store.Writer
	comp int64
	runs map[string]int64
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	f := &releaseFixture{s: s, w: store.NewWriter(s, 0), runs: map[string]int64{}}
	if f.comp, err = s.InsertUpstreamRepository("vendor/comp"); err != nil {
		t.Fatal(err)
	}
	f.node(t, store.Node{Key: "vendor/comp", Kind: "action", Owner: "vendor", Repo: "comp", IsExternal: true})
	f.node(t, store.Node{Key: "comp-step", Kind: "step", RepositoryID: f.comp, ContainerKey: "vendor/comp"})
	for _, a := range []struct{ owner, repo string }{{"actions", "cache"}, {"actions", "setup-go"}} {
		f.node(t, store.Node{Key: a.owner + "/" + a.repo, Kind: "action", Owner: a.owner, Repo: a.repo,
			IsExternal: true})
	}
	f.flush(t)
	f.declare(t, "c1", "actions/cache", "v3")
	f.declare(t, "c2", "actions/setup-go", "v6")
	f.resolve(t, "v1", "c1")
	f.resolve(t, "v2", "c2")
	affects(t, s, "GHSA-cache", "actions/cache", "")
	affects(t, s, "GHSA-setup-go", "actions/setup-go", "")
	return f
}

func (f *releaseFixture) node(t *testing.T, n store.Node) {
	t.Helper()
	if err := f.w.AddNode(n); err != nil {
		t.Fatal(err)
	}
}

func (f *releaseFixture) flush(t *testing.T) {
	t.Helper()
	if err := f.w.Flush(); err != nil {
		t.Fatal(err)
	}
}

// declare writes what vendor/comp's step says at its one location, as the edges run at a commit.
func (f *releaseFixture) declare(t *testing.T, commit, dst, ref string) {
	t.Helper()
	run, err := f.s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
		QueryID: "abom/edges", Repository: "vendor/comp", DBCommit: commit})
	if err != nil {
		t.Fatal(err)
	}
	f.runs[commit] = run
	if err := f.w.AddEdge(store.Edge{Key: "vendor/comp:action.yml:15:11", SrcKey: "comp-step", DstKey: dst,
		Kind: store.EdgeKindCompositeStepUsesAction, RefRaw: dst + "@" + ref, RefValue: ref,
		RepositoryID: f.comp, QueryRunID: run, FilePath: "action.yml", StartLine: 15, StartColumn: 11,
	}); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
}

// resolve records that vendor/comp's reference resolved to a commit.
func (f *releaseFixture) resolve(t *testing.T, ref, commit string) {
	t.Helper()
	if _, err := f.s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
		                           resolved_sha, analyzed_ref)
		VALUES ('vendor/comp', ?, 'vendor', 'comp', 1, 'done', '2026-09-27T00:00:00Z', ?, ?)`,
		ref, commit, ref); err != nil {
		t.Fatal(err)
	}
}

// pins adds a repository under review whose workflow names vendor/comp at a release.
func (f *releaseFixture) pins(t *testing.T, repository, ref string) {
	t.Helper()
	repo, err := f.s.UpsertRepository(repository, "")
	if err != nil {
		t.Fatal(err)
	}
	workflow := repository + "/.github/workflows/ci.yml"
	step := repository + "-step"
	f.node(t, store.Node{Key: workflow, Kind: "workflow", RepositoryID: repo})
	f.node(t, store.Node{Key: step, Kind: "step", RepositoryID: repo, ContainerKey: workflow})
	if err := f.w.AddEdge(store.Edge{Key: repository + ":.github/workflows/ci.yml:9:9", SrcKey: step,
		DstKey: "vendor/comp", Kind: store.EdgeKindStepUsesAction, RefRaw: "vendor/comp@" + ref,
		RefValue: ref, RepositoryID: repo, FilePath: ".github/workflows/ci.yml", StartLine: 9,
		StartColumn: 9}); err != nil {
		t.Fatal(err)
	}
	f.flush(t)
}

// A route through a dependency stays inside the release a repository pinned, and that release's
// children are what its own commit declared there. Read from the shared row instead, the release
// pinned at c1 reports c2's actions/setup-go, and actions/cache — which it really runs — vanishes.
func TestExposureThroughAPinnedReleaseFollowsWhatThatReleaseDeclared(t *testing.T) {
	f := newReleaseFixture(t)
	f.pins(t, "octo/app", "v1")
	f.pins(t, "octo/other", "v2")

	app, other := exposedFrom(t, f.s, "octo/app"), exposedFrom(t, f.s, "octo/other")
	if _, ok := app["actions/cache"]; !ok {
		t.Errorf("octo/app pins the release that runs actions/cache and is not exposed to it: %v", app)
	}
	if _, ok := app["actions/setup-go"]; ok {
		t.Errorf("octo/app is exposed to actions/setup-go, which only the release it does not pin runs: %v", app)
	}
	if _, ok := other["actions/setup-go"]; !ok {
		t.Errorf("octo/other pins the release that runs actions/setup-go and is not exposed to it: %v", other)
	}
	if _, ok := other["actions/cache"]; ok {
		t.Errorf("octo/other is exposed to actions/cache, which only the release it does not pin runs: %v", other)
	}
}

// A sighting recorded before content was kept says the commit declared something at the location,
// and not what. Guessing from the row would name the last writer's target, so it gives no route.
func TestASightingWhoseContentIsNotKnownGivesNoRouteRatherThanAWrongOne(t *testing.T) {
	f := newReleaseFixture(t)
	f.pins(t, "octo/app", "v1")
	if _, err := f.s.DB().Exec(`UPDATE abom_edge_sighting
		SET dst_node_row_id = NULL, ref_raw = NULL, ref_value = NULL WHERE query_run_id = ?`,
		f.runs["c1"]); err != nil {
		t.Fatal(err)
	}

	if app := exposedFrom(t, f.s, "octo/app"); len(app) != 0 {
		t.Errorf("octo/app's release declared nothing known, and it is exposed to %v", app)
	}
}

// A commit no reference resolves to any more is history. What it declared is not what anyone
// runs, so it neither reaches anything nor is reached through.
func TestACommitNothingResolvesToAnyMoreIsNotWalked(t *testing.T) {
	f := newReleaseFixture(t)
	f.pins(t, "octo/app", "v1")
	if _, err := f.s.DB().Exec(`UPDATE abom_frontier SET resolved_sha = 'c3' WHERE ref = 'v1'`); err != nil {
		t.Fatal(err)
	}
	f.declare(t, "c3", "actions/setup-go", "v6")

	dependents, err := (&Builder{Store: f.s}).Dependents("actions/cache")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(dependents, "octo/app") {
		t.Errorf("octo/app reaches actions/cache through c1, which nothing resolves to: %v", dependents)
	}
	if app := exposedFrom(t, f.s, "octo/app"); app["actions/setup-go"] == 0 {
		t.Errorf("octo/app's release now resolves to c3, which runs actions/setup-go: %v", app)
	}
}

// A release whose content is identical to one already analyzed is settled without analysis, so no
// run sighted anything at its commit. It runs what the identical commit declared.
func TestAReleaseWithIdenticalContentRunsWhatTheAnalyzedCommitDeclared(t *testing.T) {
	f := newReleaseFixture(t)
	f.resolve(t, "v1.0.1", "c1-rebuilt")
	for _, c := range []string{"c1", "c1-rebuilt"} {
		if err := f.s.RecordCommitContent("vendor/comp", c, "same-tree"); err != nil {
			t.Fatal(err)
		}
	}
	f.pins(t, "octo/patched", "v1.0.1")

	if app := exposedFrom(t, f.s, "octo/patched"); app["actions/cache"] == 0 {
		t.Errorf("octo/patched pins a release identical to c1, which runs actions/cache: %v", app)
	}
}

// The submission closure and the exposure walk answer one question from two sides, and a repository
// submitted with a dependency it is not exposed through is a contradiction GitHub would act on. Both
// read what the pinned release declared, including a release settled as identical to another.
func TestBothWalksFollowWhatThePinnedReleaseDeclared(t *testing.T) {
	f := newReleaseFixture(t)
	f.resolve(t, "v1.0.1", "c1-rebuilt")
	for _, c := range []string{"c1", "c1-rebuilt"} {
		if err := f.s.RecordCommitContent("vendor/comp", c, "same-tree"); err != nil {
			t.Fatal(err)
		}
	}
	f.pins(t, "octo/app", "v1")
	f.pins(t, "octo/other", "v2")
	f.pins(t, "octo/patched", "v1.0.1")

	for _, c := range []struct{ repository, runs, notRuns string }{
		{"octo/app", "actions/cache", "actions/setup-go"},
		{"octo/other", "actions/setup-go", "actions/cache"},
		{"octo/patched", "actions/cache", "actions/setup-go"},
	} {
		exposed, submitted := exposedFrom(t, f.s, c.repository), submittedFrom(t, f.s, c.repository)
		if _, ok := exposed[c.runs]; !ok || !submitted[c.runs] {
			t.Errorf("%s runs %s: exposed %v, submitted %v", c.repository, c.runs, exposed, submitted)
		}
		if _, ok := exposed[c.notRuns]; ok || submitted[c.notRuns] {
			t.Errorf("%s does not run %s: exposed %v, submitted %v", c.repository, c.notRuns, exposed, submitted)
		}
	}
}
