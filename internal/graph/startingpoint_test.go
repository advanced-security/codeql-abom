package graph

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// startingPoint records repositories as the crawl does: one edges run per commit, each sighting
// carrying what that commit declared, and a queue row for every commit something stands at.
type startingPoint struct {
	s    *store.Store
	w    *store.Writer
	runs map[string]int64
}

func newStartingPoint(t *testing.T) *startingPoint {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	return &startingPoint{s: s, w: store.NewWriter(s, 0), runs: map[string]int64{}}
}

// startingPointOf records into a store some fixture already built.
func startingPointOf(s *store.Store) *startingPoint {
	return &startingPoint{s: s, w: store.NewWriter(s, 0), runs: map[string]int64{}}
}

// queued records a queue row resolved to a commit: depth 0 for a repository's own branch, deeper
// for a reference somebody made to it.
func (f *startingPoint) queued(t *testing.T, repository, ref string, depth int, commit string) {
	t.Helper()
	owner, repo, _ := strings.Cut(repository, "/")
	exec(t, f.s, `INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
	                                         resolved_sha)
		VALUES (?, ?, ?, ?, ?, 'done', '2026-10-01T00:00:00Z', ?)`,
		repository, ref, owner, repo, depth, commit)
}

// declares records that a file of repository names dst@ref at a line, at a commit. action.yml is
// the repository's own action, which is what a consumer's `uses:` names; anything else a workflow.
func (f *startingPoint) declares(t *testing.T, repository, commit, file string, line int, dst, ref string) {
	t.Helper()
	repo, err := f.s.UpsertRepository(repository, "")
	if err != nil {
		t.Fatal(err)
	}
	run, ok := f.runs[repository+"@"+commit]
	if !ok {
		if run, err = f.s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
			QueryID: "abom/edges", Repository: repository, DBCommit: commit}); err != nil {
			t.Fatal(err)
		}
		if err := f.s.MarkSightedContent(run); err != nil {
			t.Fatal(err)
		}
		f.runs[repository+"@"+commit] = run
	}
	container, kind := repository+"/"+file, store.EdgeKindStepUsesAction
	nodes := []store.Node{{Key: container, Kind: "workflow", RepositoryID: repo, Path: file}}
	if file == "action.yml" {
		owner, name, _ := strings.Cut(repository, "/")
		container, kind = repository, store.EdgeKindCompositeStepUsesAction
		nodes = []store.Node{{Key: container, Kind: "action", Owner: owner, Repo: name, IsExternal: true}}
	}
	step := fmt.Sprintf("%s:step:%d", container, line)
	owner, name, _ := strings.Cut(dst, "/")
	nodes = append(nodes,
		store.Node{Key: step, Kind: "step", RepositoryID: repo, ContainerKey: container},
		store.Node{Key: dst, Kind: "action", Owner: owner, Repo: name, IsExternal: true})
	for _, n := range nodes {
		if err := f.w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.w.AddEdge(store.Edge{Key: fmt.Sprintf("%s:%s:%d:9", repository, file, line),
		SrcKey: step, DstKey: dst, Kind: kind, RefRaw: dst + "@" + ref, RefValue: ref,
		RepositoryID: repo, QueryRunID: run, FilePath: file, StartLine: line, StartColumn: 9,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.w.Flush(); err != nil {
		t.Fatal(err)
	}
}

// toolFixture is a repository under review that is also somebody's dependency:
//
//	acme/app   ci.yml     at a1, its default branch -> acme/tool@v1, which resolves to r1
//	acme/tool  ci.yml     at d1, its default branch -> actions/checkout@v4
//	acme/tool  ci.yml     at r1, the release v1     -> actions/checkout@v3, actions/cache@v2
//	acme/tool  action.yml at d1 and at r1           -> actions/setup-node@v4
//
// r1 is live because acme/app runs acme/tool@v1, which is the action and not acme/tool's CI.
// Without defaultBranch, acme/tool's default branch has no commit on record and d1 is history.
func toolFixture(t *testing.T, defaultBranch bool) *startingPoint {
	t.Helper()
	f := newStartingPoint(t)
	f.declares(t, "acme/app", "a1", ".github/workflows/ci.yml", 9, "acme/tool", "v1")
	f.declares(t, "acme/tool", "d1", ".github/workflows/ci.yml", 10, "actions/checkout", "v4")
	f.declares(t, "acme/tool", "d1", "action.yml", 5, "actions/setup-node", "v4")
	f.declares(t, "acme/tool", "r1", ".github/workflows/ci.yml", 12, "actions/checkout", "v3")
	f.declares(t, "acme/tool", "r1", ".github/workflows/ci.yml", 14, "actions/cache", "v2")
	f.declares(t, "acme/tool", "r1", "action.yml", 5, "actions/setup-node", "v4")
	f.queued(t, "acme/app", "", 0, "a1")
	f.queued(t, "acme/tool", "v1", 1, "r1")
	if defaultBranch {
		f.queued(t, "acme/tool", "", 0, "d1")
	}
	for _, pkg := range []string{"acme/tool", "actions/checkout", "actions/cache",
		"actions/setup-node", "actions/upload-artifact"} {
		affects(t, f.s, "GHSA-"+pkg, pkg, "")
	}
	return f
}

// reachedFrom names the dependencies stored reach says a repository runs.
func reachedFrom(t *testing.T, s *store.Store, repository string) map[string]bool {
	t.Helper()
	rows, err := s.DB().Query(`
		SELECT n.node_key FROM abom_node_reach x
		  JOIN abom_node n ON n.row_id = x.node_row_id AND n.is_external = 1
		  JOIN repository r ON r.row_id = x.repository_row_id
		 WHERE r.repository_full_name = ?`, repository)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		out[key] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func rebuilt(t *testing.T, s *store.Store) {
	t.Helper()
	if _, err := (&Builder{Store: s}).Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
}

// A repository's own workflows run at its default branch. At a release a consumer pins, its CI runs
// for nobody, so what only that release's CI declared is not something the repository runs.
func TestAStartingPointCountsOnlyWhatItsDefaultBranchDeclares(t *testing.T) {
	f := toolFixture(t, true)
	rebuilt(t, f.s)

	if got := reachedBy(t, f.s, "actions/cache"); slices.Contains(got, "acme/tool") {
		t.Errorf("actions/cache is reached by %v; only acme/tool's release CI names it", got)
	}
	if got := reachedBy(t, f.s, "actions/checkout"); !slices.Equal(got, []string{"acme/tool"}) {
		t.Errorf("actions/checkout is reached by %v, want acme/tool from its default branch", got)
	}
	var steps, refs int
	if err := f.s.DB().QueryRow(`
		SELECT b.dependent_steps, b.refs_in_use FROM abom_blast_radius b
		  JOIN abom_node n ON n.row_id = b.node_row_id WHERE n.node_key = 'actions/checkout'`).Scan(
		&steps, &refs); err != nil {
		t.Fatal(err)
	}
	if steps != 1 || refs != 1 {
		t.Errorf("actions/checkout runs from %d steps at %d references, want the default branch's "+
			"one step at v4", steps, refs)
	}
	if got := reachedBy(t, f.s, "actions/setup-node"); !slices.Equal(got, []string{"acme/app", "acme/tool"}) {
		t.Errorf("actions/setup-node is reached by %v, want acme/tool's own action and acme/app "+
			"through it", got)
	}
	if hops, _, ok := exposureOfRepo(t, f.s, "acme/tool", "GHSA-actions/cache"); ok {
		t.Errorf("acme/tool is exposed to actions/cache at %d hops through CI nobody runs", hops)
	}
}

// What a repository runs is read at its default branch, and a release somebody pins is not a guess
// at it. With no default-branch commit on record nothing is known, so it adds no route of its own;
// its action is still what its consumers run.
func TestAStartingPointWithNoDefaultBranchOnRecordCountsNothingButIsStillWalkedThrough(t *testing.T) {
	f := toolFixture(t, false)
	rebuilt(t, f.s)

	if got := reachedFrom(t, f.s, "acme/tool"); len(got) > 0 {
		t.Errorf("acme/tool reaches %v with no default-branch commit on record",
			slices.Sorted(maps.Keys(got)))
	}
	if got := reachedBy(t, f.s, "actions/setup-node"); !slices.Equal(got, []string{"acme/app"}) {
		t.Errorf("actions/setup-node is reached by %v, want acme/app through acme/tool@v1", got)
	}
	if got := reachedBy(t, f.s, "acme/tool"); !slices.Equal(got, []string{"acme/app"}) {
		t.Errorf("acme/tool is reached by %v, want acme/app", got)
	}
}

// A protected branch is where workflows run with secrets and where pull_request_target and
// schedule read from, so its commit is the repository's own as much as the default branch is.
func TestAProtectedBranchsCommitCountsAsTheStartingPoints(t *testing.T) {
	f := toolFixture(t, true)
	f.queued(t, "acme/tool", "release", 0, "p1")
	f.declares(t, "acme/tool", "p1", ".github/workflows/release.yml", 3, "actions/upload-artifact", "v4")
	rebuilt(t, f.s)

	if got := reachedBy(t, f.s, "actions/upload-artifact"); !slices.Equal(got, []string{"acme/tool"}) {
		t.Errorf("actions/upload-artifact is reached by %v, want acme/tool from its protected branch", got)
	}
	if got := reachedBy(t, f.s, "actions/cache"); slices.Contains(got, "acme/tool") {
		t.Errorf("actions/cache is reached by %v; only acme/tool's release CI names it", got)
	}
}

// A snapshot replaces what GitHub found, so the closure must name what the walk counts and the
// refusal must guard the commits the closure reads: not a release only a consumer runs, whose
// unknown declarations would otherwise refuse a snapshot that never reads them.
func TestTheSubmissionClosureAndItsRefusalReadTheCommitsTheWalkStandsAt(t *testing.T) {
	f := toolFixture(t, true)
	exec(t, f.s, `UPDATE abom_query_run SET sighted_content = 0 WHERE row_id = ?`,
		f.runs["acme/tool@r1"])
	rebuilt(t, f.s)

	for _, repository := range []string{"acme/tool", "acme/app"} {
		reached, submitted := reachedFrom(t, f.s, repository), submittedFrom(t, f.s, repository)
		if len(reached) == 0 || !maps.Equal(reached, submitted) {
			t.Errorf("%s reaches %v; its submission closure carries %v", repository,
				slices.Sorted(maps.Keys(reached)), slices.Sorted(maps.Keys(submitted)))
		}
	}
	for repository, want := range map[string][]string{
		"acme/tool": {},
		"acme/app":  {"acme/tool@r1"},
	} {
		got, err := f.s.UnobservedClosureCommits(repository)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s's snapshot is refused over %v, want %v: the commits its closure reads",
				repository, got, want)
		}
	}
}
