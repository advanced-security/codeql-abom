package store

import (
	"fmt"
	"testing"
)

const linesWorkflow = ".github/workflows/ci.yml"

// linesFixture writes `uses:` lines naming actions as edges runs at commits declare them, and
// findings on those lines as findings runs at commits report them.
type linesFixture struct {
	s     *Store
	w     *Writer
	repos map[string]int64
	edges map[string]int64
}

func newLinesFixture(t *testing.T) *linesFixture {
	t.Helper()
	f := &linesFixture{s: newTestStore(t), repos: map[string]int64{}, edges: map[string]int64{}}
	f.w = NewWriter(f.s, 0)
	add := adder(t, f.w)
	for _, a := range []struct{ owner, repo string }{{"vendor", "act"}, {"other", "act"}} {
		add(Node{Key: a.owner + "/" + a.repo, Kind: "action", Owner: a.owner, Repo: a.repo, IsExternal: true})
	}
	return f
}

// repository adds a repository with a workflow, as a starting point or as somebody's dependency.
func (f *linesFixture) repository(t *testing.T, name string, upstream bool) {
	t.Helper()
	insert := func(n string) (int64, error) { return f.s.UpsertRepository(n, "") }
	if upstream {
		insert = f.s.InsertUpstreamRepository
	}
	id, err := insert(name)
	if err != nil {
		t.Fatal(err)
	}
	f.repos[name] = id
	adder(t, f.w)(Node{Key: name + "/" + linesWorkflow, Kind: "workflow", RepositoryID: id})
}

// declare is an edges run of a repository at a commit seeing its line name dst@ref.
func (f *linesFixture) declare(t *testing.T, repository, commit string, line int, dst, ref string) {
	t.Helper()
	run, err := f.s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular, QueryID: "abom/edges",
		Repository: repository, DBCommit: commit})
	if err != nil {
		t.Fatal(err)
	}
	f.edges[repository+"@"+commit] = run
	step := fmt.Sprintf("%s-step-%d", repository, line)
	adder(t, f.w)(Node{Key: step, Kind: "step", RepositoryID: f.repos[repository],
		ContainerKey: repository + "/" + linesWorkflow})
	if err := f.w.AddEdge(Edge{Key: fmt.Sprintf("%s:%s:%d:9", repository, linesWorkflow, line),
		SrcKey: step, DstKey: dst, Kind: EdgeKindStepUsesAction, RefRaw: dst + "@" + ref, RefValue: ref,
		RepositoryID: f.repos[repository], QueryRunID: run, FilePath: linesWorkflow, StartLine: line,
		StartColumn: 9}); err != nil {
		t.Fatal(err)
	}
	if err := f.w.Flush(); err != nil {
		t.Fatal(err)
	}
}

// forget makes a commit's sightings the kind written before content was kept.
func (f *linesFixture) forget(t *testing.T, repository, commit string) {
	t.Helper()
	if _, err := f.s.DB().Exec(`UPDATE abom_edge_sighting
		SET dst_node_row_id = NULL, ref_raw = NULL, ref_value = NULL, pin_comment = NULL
		WHERE query_run_id = ?`, f.edges[repository+"@"+commit]); err != nil {
		t.Fatal(err)
	}
}

// analyze is a findings run of a repository at a commit.
func (f *linesFixture) analyze(t *testing.T, repository, commit string) int64 {
	t.Helper()
	run, err := f.s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "findings/suite.qls", Repository: repository, DBCommit: commit})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// report is a findings run at a commit reporting a rule on a line. One identity reported at two
// commits is one finding sighted twice.
func (f *linesFixture) report(t *testing.T, repository, commit string, line int, rule, identity string,
	score *float64) {
	t.Helper()
	if _, err := f.s.AddFindings([]Finding{{QueryRunID: f.analyze(t, repository, commit), RuleID: rule,
		RepositoryID: f.repos[repository], FilePath: linesWorkflow, StartLine: line, StartColumn: 9,
		Severity: "warning", SecuritySeverity: score, Fingerprint: identity, ResultHash: identity,
	}}); err != nil {
		t.Fatal(err)
	}
}

func findingsOnLines(t *testing.T, s *Store, key string, counted map[string][]string) map[string]LineFindings {
	t.Helper()
	live, err := s.LiveCommits()
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.FindingsOnLines(key, counted, live)
	if err != nil {
		t.Fatalf("FindingsOnLines(%q): %v", key, err)
	}
	return got
}

// A line is tracked across commits and names different things at different ones, so a finding
// belongs to the version only where the line named it at the commit the finding's run analyzed.
// Matched by the row, every finding on octo/app's line 9 would belong to other/act, which wrote it
// last; matched by file and line alone, v1 would carry v2's and other/act's. A commit whose
// declaration was never recorded is not guessed at, and a commit no findings run reached is not
// analyzed rather than clean.
func TestAFindingCountsForAVersionOnlyWhereItsLineNamedItAtTheFindingsCommit(t *testing.T) {
	f := newLinesFixture(t)
	f.repository(t, "octo/app", false)
	f.repository(t, "third/party", true)

	f.declare(t, "octo/app", "a3", 9, "vendor/act", "v1")
	f.forget(t, "octo/app", "a3")
	f.declare(t, "octo/app", "a0", 9, "vendor/act", "v1")
	f.declare(t, "octo/app", "a1", 9, "vendor/act", "v1")
	f.declare(t, "octo/app", "a1", 30, "vendor/act", "v4")
	f.declare(t, "octo/app", "a2", 9, "vendor/act", "v2")
	f.declare(t, "octo/app", "a4", 20, "vendor/act", "v3")
	f.declare(t, "octo/app", "a5", 9, "other/act", "v1")
	f.declare(t, "third/party", "a1", 9, "vendor/act", "v1")

	f.report(t, "octo/app", "a3", 9, "actions/unpinned-tag", "x3", score(5))
	f.report(t, "octo/app", "a0", 9, "actions/unpinned-tag", "x1", score(5))
	f.report(t, "octo/app", "a1", 9, "actions/unpinned-tag", "x1", score(5))
	f.report(t, "octo/app", "a1", 9, "actions/untrusted-checkout/high", "y1", score(8))
	f.report(t, "octo/app", "a2", 9, "actions/unpinned-tag", "x2", nil)
	f.report(t, "octo/app", "a5", 9, "actions/unpinned-tag", "x5", score(5))
	f.report(t, "third/party", "a1", 9, "actions/unpinned-tag", "z", score(9.5))

	// third/party is asked about only for v9, which it never names, so its line naming v1 is read
	// and must still not count for v1.
	app := []string{"octo/app"}
	got := findingsOnLines(t, f.s, "vendor/act", map[string][]string{"v1": app, "v2": app, "v3": app, "v4": app,
		"v9": {"third/party"}})

	want := map[string]LineFindings{
		"v1": {Count: 2, Severity: SeverityCounts{High: 1, Medium: 1}, Commits: 2, Analyzed: 2, Unknown: 1},
		"v2": {Count: 1, Severity: SeverityCounts{Unknown: 1}, Commits: 1, Analyzed: 1, Unknown: 1},
		"v4": {Commits: 1, Analyzed: 1},
	}
	for ref, w := range want {
		if g, ok := got[ref]; !ok || g != w {
			t.Errorf("%s: %+v (present %v), want %+v", ref, g, ok, w)
		}
	}
	if g, ok := got["v3"]; ok {
		t.Errorf("v3 reports %+v; no findings run reached a4, where its line named it, so it is not analyzed", g)
	}
	if len(got) != len(want) {
		t.Errorf("answered %d references, want %d: %+v", len(got), len(want), got)
	}

	// The repositories named are the ones whose lines count: third/party's finding is its own.
	upstream := findingsOnLines(t, f.s, "vendor/act", map[string][]string{"v1": {"third/party"}})
	if g := upstream["v1"]; g.Count != 1 || g.Severity.Critical != 1 || g.Unknown != 0 {
		t.Errorf("v1 on third/party's lines: %+v, want its one critical finding", g)
	}
}

// The walk does not stand at a commit no reference resolves to any more, so the uses: lines column
// does not count what a line declared there, and neither may the findings on it.
func TestAFindingAtACommitNothingRunsAnyMoreIsNotOnAVersionsLines(t *testing.T) {
	f := newLinesFixture(t)
	f.repository(t, "octo/lib", false)
	if _, err := f.s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at, resolved_sha)
		VALUES ('octo/lib', 'v9', 'octo', 'lib', 1, 'done', '2026-09-27T00:00:00Z', 'b2')`); err != nil {
		t.Fatal(err)
	}
	f.declare(t, "octo/lib", "b1", 9, "vendor/act", "v1")
	f.declare(t, "octo/lib", "b2", 9, "vendor/act", "v1")
	f.report(t, "octo/lib", "b1", 9, "actions/unpinned-tag", "old", score(5))
	f.report(t, "octo/lib", "b2", 9, "actions/unpinned-tag", "new", score(5))

	got := findingsOnLines(t, f.s, "vendor/act", map[string][]string{"v1": {"octo/lib"}})
	if g := got["v1"]; g.Count != 1 || g.Commits != 1 {
		t.Errorf("v1: %+v, want only the finding at b2, which octo/lib@v9 resolves to", g)
	}
}

// Nothing asked is nothing answered, and an empty answer is a map rather than nil.
func TestFindingsOnLinesOfNoRepositoryIsEmpty(t *testing.T) {
	f := newLinesFixture(t)
	got := findingsOnLines(t, f.s, "vendor/act", map[string][]string{"v1": nil})
	if got == nil || len(got) != 0 {
		t.Errorf("got %+v, want an empty map", got)
	}
}
