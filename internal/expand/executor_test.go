package expand

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// executorFixture is a run over three repositories: two with a database on disk and one with
// none, which is the shape every real run has.
func executorFixture(t *testing.T) (*store.Store, *Executor, int64, *fakeAnalyzer) {
	t.Helper()
	h := newHarness(t)
	root := t.TempDir()
	query := queryInPack(t, root, "codeql/actions-queries", "0.6.35", "Unpinned.ql", "select 1")

	analyzer := &fakeAnalyzer{}
	e := &Executor{
		Store: h.store,
		Resolver: &fakeResolver{
			queries: []string{query},
			meta:    map[string]runner.Metadata{query: {ID: "actions/unpinned-tag", Kind: "problem"}},
		},
		Analyzer: analyzer,
		Ingester: h.expander.Ingester,
		Suites:   []string{"abom-findings.qls"},
		WorkDir:  filepath.Join(root, "runs"),
	}

	held := func(repo, sha string) store.ScopedDatabase {
		dir := filepath.Join(root, "db", repo)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("preparing %s: %v", dir, err)
		}
		return store.ScopedDatabase{
			DatabaseIdentity: store.DatabaseIdentity{
				Repository: repo, Commit: sha, SourceFilter: "f0", Mode: store.AnalysisModeRegular,
			},
			Status: store.DatabaseRetained, Path: dir,
		}
	}
	id, err := h.store.CreateAnalysisRun(store.AnalysisRequest{
		RequestedBy: "operator",
		Predicate:   "rules=actions%2F*",
		Mode:        store.AnalysisModeRegular,
		Queries:     []store.AnalysisQuery{{QueryID: "actions/unpinned-tag"}},
		Databases: []store.ScopedDatabase{
			held("octo-app", "sha-one"),
			held("octo-tool", "sha-two"),
			{DatabaseIdentity: store.DatabaseIdentity{
				Repository: "octo/absent", Commit: "sha-three",
				SourceFilter: "f0", Mode: store.AnalysisModeRegular,
			}, Status: store.DatabaseBuildable},
		},
	})
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	return h.store, e, id, analyzer
}

func membersByRepository(t *testing.T, s *store.Store, id int64) map[string]store.AnalysisMember {
	t.Helper()
	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	by := map[string]store.AnalysisMember{}
	for _, m := range run.Members {
		by[m.Repository] = m
	}
	return by
}

// The run analyzes what is on disk and says so per repository, which is the whole point of
// retaining databases.
func TestARunAnalyzesEveryMemberWhoseDatabaseIsHeld(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	by := membersByRepository(t, s, id)
	for _, repo := range []string{"octo-app", "octo-tool"} {
		if by[repo].Status != store.AnalysisSucceeded {
			t.Errorf("%s is %q, want %q", repo, by[repo].Status, store.AnalysisSucceeded)
		}
	}
	if len(analyzer.targets) != 2 {
		t.Errorf("analyze ran %d times, want once per held database", len(analyzer.targets))
	}
}

// A member with nothing on disk was not attempted, and saying so is what stops a completed run
// reading as a clean result for a repository nothing ran against.
func TestAMemberWithNoDatabaseIsReportedRatherThanLeftPending(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	absent := membersByRepository(t, s, id)["octo/absent"]
	if absent.Status != store.AnalysisNoDatabase {
		t.Errorf("a member with no database is %q, want %q",
			absent.Status, store.AnalysisNoDatabase)
	}
	if absent.Detail == "" {
		t.Error("a member nothing could analyze does not say why")
	}
}

// The raw output is the evidence a tuning decision rests on, and re-running to see it again costs
// what the run cost.
func TestASucceededMemberNamesWhereItsResultsWereWritten(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	member := membersByRepository(t, s, id)["octo-app"]
	if member.ResultPath == "" {
		t.Fatal("a succeeded member names no results")
	}
	if _, err := os.Stat(member.ResultPath); err != nil {
		t.Errorf("the recorded result path is not there: %v", err)
	}
}

// How much a version found is the number a reader compares two versions by, and re-reading every
// member's SARIF to get it costs the run again on a page load.
func TestASucceededMemberRecordsHowMuchItFound(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	by := membersByRepository(t, s, id)
	member := by["octo-app"]
	if !member.Counted || member.Results != 2 {
		t.Errorf("an analyzed member reports counted=%v results=%d, want the 2 in the fixture",
			member.Counted, member.Results)
	}
	// A member nothing ran against has no count. Reporting its zero would say a query found
	// nothing in a repository it never looked at.
	if by["octo/absent"].Counted {
		t.Error("a member with no database reports a count")
	}

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if run.Results != 4 {
		t.Errorf("the run totals %d findings, want 4 across its two analyzed members", run.Results)
	}
}

// A result nothing ingested is a result only the run can see: the graph, the Inventory, a policy
// and the gate all read the findings table, so a run that writes SARIF and stops reports numbers
// no other surface can account for.
func TestWhatARunFoundReachesTheTablesEverythingElseReads(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, repo := range []string{"octo-app", "octo-tool"} {
		var n int
		if err := s.DB().QueryRow(`
			SELECT COUNT(*) FROM abom_finding f
			JOIN repository r ON r.row_id = f.repository_row_id
			WHERE r.repository_full_name = ?`, repo).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Errorf("%s carries %d findings in the table, want the 2 its SARIF reported", repo, n)
		}
	}
}

// Every stored fact points at a run, so any row can be traced to the query and pack version that
// produced it. A finding attributed to no run is one nobody can re-derive or invalidate.
func TestAnIngestedFindingNamesTheEvaluationThatProducedIt(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var queryID, version, cli string
	if err := s.DB().QueryRow(`
		SELECT q.query_id, COALESCE(q.query_version, ''), COALESCE(q.cli_version, '')
		FROM abom_finding f JOIN abom_query_run q ON q.row_id = f.query_run_id
		LIMIT 1`).Scan(&queryID, &version, &cli); err != nil {
		t.Fatalf("a finding names no run: %v", err)
	}
	if queryID != "actions/unpinned-tag" {
		t.Errorf("the run names %q, want the query the request asked for", queryID)
	}
	if cli == "" {
		t.Error("the run records no CodeQL version, so a later skip cannot tell it apart")
	}
}

// A run of one query records that query's own version, which is what makes it joinable to the
// catalog. A hash of a one-element list is an identity nothing else holds, so the run would
// count against no version and the catalog would report the query as never analyzed.
func TestARunOfOneQueryRecordsThatQuerysOwnVersion(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var recorded string
	if err := s.DB().QueryRow(`
		SELECT COALESCE(query_version, '') FROM abom_query_run
		WHERE query_id = 'actions/unpinned-tag' LIMIT 1`).Scan(&recorded); err != nil {
		t.Fatalf("no run for the query the request named: %v", err)
	}

	want, err := runner.QueryVersion(e.Resolver.(*fakeResolver).queries[0])
	if err != nil {
		t.Fatalf("QueryVersion: %v", err)
	}
	if recorded != want {
		t.Errorf("the run records %q, want the query's own version %q", recorded, want)
	}
}

// A finding no node carries cannot be refused by a gate, drawn on the graph or counted by a unit.
// The crawl attributes at the end of a cycle; a run that skipped it would store results only the
// findings table could see.
func TestARunAttributesWhatItFoundToTheGraph(t *testing.T) {
	s, e, id, _ := executorFixture(t)
	nodeForFinding(t, s, "octo-app")

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var linked int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_finding_node`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked == 0 {
		t.Error("nothing the run found is attached to a node, so no gate or unit can see it")
	}
}

// A workflow node at the path the fixture SARIF reports, so attribution has something to match.
// The graph is built by the crawl; a run analyzes repositories the crawl has already placed.
func nodeForFinding(t *testing.T, s *store.Store, repository string) {
	t.Helper()
	repoID, err := s.UpsertRepository(repository, "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	// Both fixture results sit in this file, and a workflow node is what the second attribution
	// pass matches on — most rules report on a workflow rather than on a `uses:` line.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_node (node_key, node_kind, repository_row_id, path)
		VALUES (?, 'workflow', ?, '.github/workflows/main.yml')`,
		repository+":workflow", repoID); err != nil {
		t.Fatalf("placing a node: %v", err)
	}
}

// One repository failing is not the run failing. Stopping would discard the work already done and
// leave a reader unable to tell a broken query from a broken repository.
func TestOneMemberFailingDoesNotStopTheRest(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)
	analyzer.failOn = "octo-app"

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	by := membersByRepository(t, s, id)
	if by["octo-app"].Status != store.AnalysisFailed {
		t.Errorf("the failing member is %q, want %q", by["octo-app"].Status, store.AnalysisFailed)
	}
	if by["octo-app"].Detail == "" {
		t.Error("the failing member does not say what went wrong")
	}
	if by["octo-tool"].Status != store.AnalysisSucceeded {
		t.Errorf("the other member is %q; one failure stopped the run",
			by["octo-tool"].Status)
	}
}

// A run reports complete once nothing is left to attempt, whatever became of its members: a run
// that only completed on total success could never finish over a real corpus.
func TestARunCompletesEvenWhenSomeMembersFailed(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)
	analyzer.failOn = "octo-app"

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if run.State != store.RunComplete {
		t.Errorf("the run is %q, want %q", run.State, store.RunComplete)
	}
	if run.FinishedAt == "" {
		t.Error("a complete run does not say when it finished")
	}
	total := 0
	for _, n := range run.Tally {
		total += n
	}
	if total != len(run.Members) {
		t.Errorf("the tally accounts for %d of %d members", total, len(run.Members))
	}
}

// A query id the configured suites do not resolve to is refused before anything runs. Running the
// rest would report a result for a request that was not carried out.
func TestAQueryTheSuitesDoNotOfferIsRefusedBeforeAnythingRuns(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)
	if _, err := s.DB().Exec(
		`UPDATE abom_analysis_query SET query_id = ?`, "nobody/writes-this"); err != nil {
		t.Fatalf("renaming the query: %v", err)
	}

	err := e.Run(context.Background(), id)
	if err == nil {
		t.Fatal("a run naming a query nothing offers was carried out")
	}
	if len(analyzer.targets) != 0 {
		t.Errorf("analyze ran %d times for a refused run, want none", len(analyzer.targets))
	}
	run, _ := s.AnalysisRun(id)
	if run.State == store.RunComplete {
		t.Error("a refused run reports complete")
	}
}

// Cancellation stops the run rather than carrying on through a corpus somebody has walked away
// from.
func TestACanceledRunStopsRatherThanFinishingTheCorpus(t *testing.T) {
	s, e, id, _ := executorFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := e.Run(ctx, id); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled run returned %v, want a cancellation", err)
	}
	run, _ := s.AnalysisRun(id)
	if run.State != store.RunCanceled {
		t.Errorf("a canceled run is %q, want %q", run.State, store.RunCanceled)
	}
}
