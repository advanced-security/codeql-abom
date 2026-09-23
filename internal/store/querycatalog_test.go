package store

import (
	"testing"
)

func catalogStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	runs := []QueryRun{
		{QueryID: "abom/edges", QueryVersion: "v1", Pack: "advanced-security/abom-actions-graph-extraction-src",
			PackVersion: "2.27.0", CLIVersion: "2.27.0", Repository: "octo/app", DBCommit: "sha-one"},
		{QueryID: "abom/edges", QueryVersion: "v1", Pack: "advanced-security/abom-actions-graph-extraction-src",
			PackVersion: "2.27.0", CLIVersion: "2.27.0", Repository: "octo/tool", DBCommit: "sha-two"},
		{QueryID: "abom/edges", QueryVersion: "v2", Pack: "advanced-security/abom-actions-graph-extraction-src",
			PackVersion: "2.27.0", CLIVersion: "2.27.0", Repository: "octo/app", DBCommit: "sha-one"},
		{QueryID: "actions/unpinned-tag", QueryVersion: "u1", Pack: "codeql/actions-queries",
			PackVersion: "0.6.35", CLIVersion: "2.27.0", Repository: "octo/app", DBCommit: "sha-one"},
	}
	for i := range runs {
		runs[i].AnalysisMode = AnalysisModeRegular
		if _, err := s.RecordQueryRun(runs[i]); err != nil {
			t.Fatalf("RecordQueryRun: %v", err)
		}
	}
	// What the suites resolved to, which is where a query's own identity comes from: the ledger's
	// unit is an evaluation, and the findings lane evaluates a whole suite at once.
	if err := s.RecordSuiteContents("abom-dependency-graph.qls", []SuiteQuery{
		{Pack: "advanced-security/abom-actions-graph-extraction-src", PackVersion: "2.27.0",
			QueryID: "abom/edges", QueryVersion: "v1", Kind: ""},
	}); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}
	if err := s.RecordSuiteContents("abom-findings.qls", []SuiteQuery{
		{Pack: "codeql/actions-queries", PackVersion: "0.6.35",
			QueryID: "actions/unpinned-tag", QueryVersion: "u1", Kind: "problem"},
	}); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}
	return s
}

func catalogBy(t *testing.T, s *Store) map[string]CatalogQuery {
	t.Helper()
	rows, err := s.QueryCatalog()
	if err != nil {
		t.Fatalf("QueryCatalog: %v", err)
	}
	by := map[string]CatalogQuery{}
	for _, r := range rows {
		by[r.QueryID+"@"+r.QueryVersion] = r
	}
	return by
}

// The unit of the catalog is a query *version*, not a query. Two versions answer differently, so
// collapsing them would report one readiness for two things.
func TestTheCatalogHoldsOneEntryPerQueryVersion(t *testing.T) {
	by := catalogBy(t, catalogStore(t))
	for _, want := range []string{"abom/edges@v1", "abom/edges@v2", "actions/unpinned-tag@u1"} {
		if _, ok := by[want]; !ok {
			t.Errorf("the catalog has no entry for %s", want)
		}
	}
	if len(by) != 3 {
		t.Errorf("the catalog holds %d entries, want one per query version", len(by))
	}
}

// How much a version has been exercised is the evidence a readiness decision rests on, so it has
// to count the databases rather than the runs: one query against two repositories is two runs and
// two repositories, and against one repository twice it is two runs and one.
func TestACatalogEntryCountsTheRepositoriesItHasRunAgainst(t *testing.T) {
	by := catalogBy(t, catalogStore(t))
	if got := by["abom/edges@v1"].Repositories; got != 2 {
		t.Errorf("abom/edges@v1 ran against %d repositories, want 2", got)
	}
	if got := by["abom/edges@v2"].Repositories; got != 1 {
		t.Errorf("abom/edges@v2 ran against %d repositories, want 1", got)
	}
}

// A query's provenance comes from the pack that supplied it. The `abom/` rule-id prefix is a
// naming convention anybody can adopt and is never evidence of where a query came from.
func TestOriginIsReadFromThePackRatherThanTheRuleId(t *testing.T) {
	by := catalogBy(t, catalogStore(t))
	if got := by["abom/edges@v1"].Origin; got != QueryOriginAbom {
		t.Errorf("abom/edges came from an ABOM pack and is recorded as %q", got)
	}
	if got := by["actions/unpinned-tag@u1"].Origin; got != QueryOriginUpstream {
		t.Errorf("actions/unpinned-tag came from a codeql/ pack and is recorded as %q", got)
	}
}

// A lane that ran rules from several packs has no single origin. Measured on the crawled
// organization: the findings lane records two packs, and reading the first called an ABOM suite
// upstream -- a provenance claim decided by which pack sorted first.
func TestALaneSpanningPacksIsMixedRatherThanTheFirstPacksOrigin(t *testing.T) {
	s := catalogStore(t)
	if _, err := s.RecordQueryRun(QueryRun{
		AnalysisMode: AnalysisModeRegular,
		QueryID:      "abom-findings.qls",
		QueryVersion: "f1",
		Pack:         "codeql/actions-queries advanced-security/abom-actions-findings-src",
		Repository:   "octo/app", DBCommit: "sha-one",
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if got := catalogBy(t, s)["abom-findings.qls@f1"].Origin; got != QueryOriginMixed {
		t.Errorf("a lane drawing from two packs reports origin %q, want %q", got, QueryOriginMixed)
	}
}

// A query that builds the graph is not one somebody develops, and a catalog that mixes them buries
// the handful worth reviewing under the crawl's own machinery. Read from the recorded `@kind`: a
// table query yields BQRS and no SARIF, which is what makes it discovery.
func TestADiscoveryQueryIsMarkedFromItsRecordedKind(t *testing.T) {
	s := catalogStore(t)

	by := catalogBy(t, s)
	if !by["abom/edges@v1"].Discovery {
		t.Error("a table query is not marked as discovery, so it will be listed beside the ones " +
			"somebody is developing")
	}
	if by["actions/unpinned-tag@u1"].Discovery {
		t.Error("a problem query is marked as discovery")
	}
}

// A query nobody resolved a suite for has an unknown kind, which is not the same as a recorded
// empty one. Treating the two alike would hide a query because nothing had looked at it.
//
// A *version* is not the unit here: a query's kind does not change between versions, so an older
// version inherits what its id was recorded as. What must stay unknown is a query id nothing has
// ever described.
func TestAQueryWithNoRecordedKindIsNotCalledDiscovery(t *testing.T) {
	s := catalogStore(t)
	if _, err := s.RecordQueryRun(QueryRun{
		AnalysisMode: AnalysisModeRegular, QueryID: "nobody/described-this",
		QueryVersion: "x1", Repository: "octo/app", DBCommit: "sha-one",
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	entry := catalogBy(t, s)["nobody/described-this@x1"]
	if entry.QueryID == "" {
		t.Fatal("a query the ledger ran is missing from the catalog")
	}
	if entry.KindRecorded {
		t.Error("a kind was reported for a query no suite resolution has described")
	}
	if entry.Discovery {
		t.Error("a query with no recorded kind was called discovery, so it would be hidden " +
			"because nothing had looked at it")
	}
}

// What the findings lane invokes is a suite, not a query. One column holding both leaves a reader
// unable to tell a rule from the set it arrived in.
func TestASuiteIsReportedApartFromTheQueryName(t *testing.T) {
	s := catalogStore(t)
	if _, err := s.RecordQueryRun(QueryRun{
		AnalysisMode: AnalysisModeRegular,
		QueryID:      "advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls",
		QueryVersion: "f1",
		Repository:   "octo/app", DBCommit: "sha-one",
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	for _, entry := range mustCatalog(t, s) {
		if entry.QueryVersion != "f1" {
			continue
		}
		if entry.Suite == "" {
			t.Error("a suite invocation reports no suite")
		}
		if entry.QueryID != "abom-findings.qls" {
			t.Errorf("the query column shows %q, want the suite's own name", entry.QueryID)
		}
		return
	}
	t.Fatal("the suite invocation is missing from the catalog")
}

func mustCatalog(t *testing.T, s *Store) []CatalogQuery {
	t.Helper()
	rows, err := s.QueryCatalog()
	if err != nil {
		t.Fatalf("QueryCatalog: %v", err)
	}
	return rows
}

// What the findings lane invokes is a suite, not a query. Now that each query in it has a row of
// its own, the lane's own row duplicates them and is marked so a catalog of queries can leave it
// out -- without dropping it, because it is still what ran.
func TestAWholeSuiteInvocationIsMarkedAsALaneRatherThanAQuery(t *testing.T) {
	s := catalogStore(t)
	if _, err := s.RecordQueryRun(QueryRun{
		AnalysisMode: AnalysisModeRegular,
		QueryID:      "advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls",
		QueryVersion: "f1",
		Repository:   "octo/app", DBCommit: "sha-one",
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	for _, entry := range mustCatalog(t, s) {
		if entry.QueryVersion != "f1" {
			continue
		}
		if !entry.Lane {
			t.Error("a whole-suite invocation is not marked as a lane, so it is listed as a query")
		}
		return
	}
	t.Fatal("the suite invocation is missing from the catalog")
}

// A multi-query ledger id is one evaluation, not a query; listed, it read as rule "acme/a acme/b".
func TestAnEvaluationOfSeveralQueriesIsALaneRatherThanAQuery(t *testing.T) {
	s := catalogStore(t)
	if _, err := s.RecordQueryRun(QueryRun{
		AnalysisMode: AnalysisModeRegular,
		QueryID:      "acme/a acme/b",
		QueryVersion: "set1",
		Pack:         "abom/custom-queries",
		Repository:   "octo/app", DBCommit: "sha-one",
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	for _, entry := range mustCatalog(t, s) {
		if entry.QueryVersion == "set1" {
			if !entry.Lane {
				t.Errorf("an evaluation of several queries is listed as the query %q", entry.QueryID)
			}
			return
		}
	}
	t.Fatal("the evaluation is missing from the catalog, so what ran is understated")
}

// A query's kind does not change with its version. An older version that predates suite-contents
// recording must inherit it, or one query is half hidden and half listed.
func TestAnOlderVersionInheritsTheKindRecordedForItsQueryId(t *testing.T) {
	s := catalogStore(t)
	if err := s.RecordSuiteContents("abom-dependency-graph.qls", []SuiteQuery{
		{Pack: "advanced-security/abom-actions-graph-extraction-src",
			PackVersion: "2.27.0", QueryID: "abom/edges", QueryVersion: "v2", Kind: ""},
	}); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}

	var older CatalogQuery
	for _, entry := range mustCatalog(t, s) {
		// v1 is in the fixture's ledger and in no suite resolution.
		if entry.QueryID == "abom/edges" && entry.QueryVersion == "v1" {
			older = entry
		}
	}
	if older.QueryID == "" {
		t.Fatal("the older version is missing from the catalog")
	}
	if !older.Discovery {
		t.Error("an older version of a table query is not marked as discovery, so one query " +
			"appears both hidden and listed")
	}
}

// A version nobody has staged has no stage. Defaulting one to `dev` would report a decision
// somebody never made, which is the same failure as an unmeasured value reported as zero.
func TestAVersionNobodyHasStagedReportsNoStage(t *testing.T) {
	by := catalogBy(t, catalogStore(t))
	if got := by["abom/edges@v1"].Stage; got != "" {
		t.Errorf("an unstaged version reports stage %q, want none", got)
	}
}

// Readiness is computed and the stage is advanced by a person, so the act is recorded with who
// did it. A stage that appeared on its own could not be told from one somebody signed.
//
// Advanced to `review` rather than `test` because `test` is the one rung that is earned: gating
// it is `querytest_test.go`'s subject, and asserting it here would make this test fail for a
// reason that has nothing to do with the audit trail.
func TestAdvancingAVersionRecordsWhoDidItAndWhen(t *testing.T) {
	s := catalogStore(t)
	if err := s.AdvanceQueryStage("abom/edges", "v1", StageReview, "operator"); err != nil {
		t.Fatalf("AdvanceQueryStage: %v", err)
	}
	entry := catalogBy(t, s)["abom/edges@v1"]
	if entry.Stage != StageReview {
		t.Errorf("the version reports stage %q, want %q", entry.Stage, StageReview)
	}
	if entry.StagedBy != "operator" || entry.StagedAt == "" {
		t.Errorf("the advance was recorded as %q at %q, want both", entry.StagedBy, entry.StagedAt)
	}
}

// Advancing again moves the same entry rather than adding a second, or the catalog would report
// one version in two stages at once.
func TestAdvancingTwiceMovesTheSameEntry(t *testing.T) {
	s := catalogStore(t)
	for _, stage := range []string{StageExtendedTest, StageReview} {
		if err := s.AdvanceQueryStage("abom/edges", "v1", stage, "operator"); err != nil {
			t.Fatalf("AdvanceQueryStage(%s): %v", stage, err)
		}
	}
	if got := catalogBy(t, s)["abom/edges@v1"].Stage; got != StageReview {
		t.Errorf("after two advances the version reports %q, want %q", got, StageReview)
	}
	if n := len(catalogBy(t, s)); n != 3 {
		t.Errorf("advancing twice left %d catalog entries, want 3", n)
	}
}

// A stage outside the lifecycle is refused. Accepting one would put a version in a state nothing
// downstream knows how to read, and the refusal is where that is cheapest to notice.
func TestAStageOutsideTheLifecycleIsRefused(t *testing.T) {
	s := catalogStore(t)
	if err := s.AdvanceQueryStage("abom/edges", "v1", "shipped", "operator"); err == nil {
		t.Error("a stage outside the lifecycle was accepted")
	}
}

// A run with no version is a row from before the column was written. It is reported as such
// rather than dropped: a catalog that silently omits them would understate what has run.
func TestRunsWithNoVersionAreStillCatalogued(t *testing.T) {
	s := catalogStore(t)
	if _, err := s.RecordQueryRun(QueryRun{
		AnalysisMode: AnalysisModeRegular, QueryID: "abom/legacy",
		Repository: "octo/app", DBCommit: "sha-one",
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if _, ok := catalogBy(t, s)["abom/legacy@"]; !ok {
		t.Error("a run recorded before query versions existed is missing from the catalog")
	}
}

func workingCatalog(t *testing.T, s *Store, working ...WorkingQuery) map[string]CatalogQuery {
	t.Helper()
	rows, err := s.QueryCatalog(working...)
	if err != nil {
		t.Fatalf("QueryCatalog: %v", err)
	}
	by := map[string]CatalogQuery{}
	for _, r := range rows {
		key := r.QueryID + "@" + r.QueryVersion
		if _, twice := by[key]; twice {
			t.Errorf("%s is listed twice", key)
		}
		by[key] = r
	}
	return by
}

// A query is written, tested and staged before anything runs it, so the catalog has to hold it
// from the moment it is on disk. Keyed on the ledger alone, it appeared only after its first run.
func TestAQueryOnDiskIsCatalogedBeforeItHasRun(t *testing.T) {
	s := newTestStore(t)

	got, ok := workingCatalog(t, s, WorkingQuery{
		QueryID: "acme/a", QueryVersion: "v1", Pack: "abom/custom-queries", Kind: "problem",
	})["acme/a@v1"]
	if !ok {
		t.Fatal("a query on disk is missing from the catalog until it has run")
	}
	if !got.Working || got.Origin != QueryOriginUser || got.Kind != "problem" || !got.KindRecorded ||
		got.Discovery || got.Runs != 0 || got.LastRunAt != "" {
		t.Errorf("listed as %+v", got)
	}
}

// Only the version on disk can be tested or edited, and every step describes it; an older version
// the ledger remembers is history.
func TestOnlyTheVersionOnDiskIsTheWorkingOne(t *testing.T) {
	s := newTestStore(t)
	for _, version := range []string{"v1", "v2"} {
		if _, err := s.RecordQueryRun(QueryRun{
			AnalysisMode: AnalysisModeRegular, QueryID: "acme/a", QueryVersion: version,
			Pack: "abom/custom-queries", Repository: "octo/app", DBCommit: "sha-" + version,
		}); err != nil {
			t.Fatal(err)
		}
	}

	by := workingCatalog(t, s, WorkingQuery{
		QueryID: "acme/a", QueryVersion: "v2", Pack: "abom/custom-queries", Kind: "problem",
	})
	if !by["acme/a@v2"].Working || by["acme/a@v2"].Runs != 1 {
		t.Errorf("the version on disk reads %+v, want the working one with its own run", by["acme/a@v2"])
	}
	if by["acme/a@v1"].Working {
		t.Error("a version no longer on disk is marked as the working one")
	}
}

// A version that has never run can still have been tested and staged, and both are what its
// readiness is read from.
func TestAVersionThatHasNeverRunCarriesItsTestsAndStage(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordQueryTest(QueryTest{
		QueryID: "acme/a", QueryVersion: "v1", Passed: true, Total: 1, Detail: "1 of 1 passed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceQueryStage("acme/a", "v1", StageTest, "operator"); err != nil {
		t.Fatal(err)
	}

	got := workingCatalog(t, s, WorkingQuery{
		QueryID: "acme/a", QueryVersion: "v1", Pack: "abom/custom-queries", Kind: "problem",
	})["acme/a@v1"]
	if !got.Tested || !got.Passing || got.Stage != StageTest || got.StagedBy != "operator" {
		t.Errorf("a never-run version reads %+v, want its passing test and its stage", got)
	}
}
