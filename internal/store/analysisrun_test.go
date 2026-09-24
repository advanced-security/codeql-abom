package store

import (
	"strings"
	"testing"
)

func memberNamed(t *testing.T, run AnalysisRun, repository string) AnalysisMember {
	t.Helper()
	for _, m := range run.Members {
		if m.Repository == repository {
			return m
		}
	}
	t.Fatalf("run %d has no member %s", run.RowID, repository)
	return AnalysisMember{}
}

// A repository ABOM tracks and nothing has analyzed names no commit to rebuild, so the run builds
// its default branch rather than reporting no database. A shape the crawl never builds stays an
// absence with its reason.
func TestARepositoryNothingHasAnalyzedIsLeftForTheRunToBuild(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertRepository("octo/unanalyzed", ""); err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	run, err := s.AnalysisRun(startedRun(t, s))
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	member := memberNamed(t, run, "octo/unanalyzed")
	if member.Status != AnalysisPending {
		t.Errorf("a tracked repository nothing analyzed is %q, want %q", member.Status, AnalysisPending)
	}
	if !strings.Contains(member.Detail, "default branch") {
		t.Errorf("the member says %q, want it to name what the run builds", member.Detail)
	}

	extended := runRequest(s)
	extended.Mode = AnalysisModeExtended
	id, err := s.CreateAnalysisRun(extended)
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	if run, err = s.AnalysisRun(id); err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if m := memberNamed(t, run, "octo/unanalyzed"); m.Status != AnalysisNoDatabase || m.Detail == "" {
		t.Errorf("an extended member nothing builds is %q (%q), want %q with a reason",
			m.Status, m.Detail, AnalysisNoDatabase)
	}
	if m := memberNamed(t, run, "octo/tool"); m.Status != AnalysisNoDatabase || m.Detail == "" {
		t.Errorf("an extended database nothing holds is %q (%q), want %q with a reason",
			m.Status, m.Detail, AnalysisNoDatabase)
	}
}

// Ingesting a repository nothing tracks would enter it as a seed, so a run never fetches one, and
// the member says why rather than reading as a database nobody built.
func TestARunNeverFetchesARepositoryABOMDoesNotTrack(t *testing.T) {
	s := newTestStore(t)
	run, err := s.AnalysisRun(startedRun(t, s))
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	member := memberNamed(t, run, "octo/unanalyzed")
	if member.Status != AnalysisNoDatabase {
		t.Errorf("an untracked repository is %q, want %q", member.Status, AnalysisNoDatabase)
	}
	if !strings.Contains(member.Detail, "tracks no repository") {
		t.Errorf("the member says %q, want it to say ABOM does not track the repository", member.Detail)
	}
}

// A re-run declines only work whose answer is stored in full: a member that finished, was counted
// and is linked to the evaluation under this exact key. A ledger row alone is not that, because an
// ingest that failed leaves one behind.
func TestAnEvaluationARunStoredAnswersTheSameKey(t *testing.T) {
	s := newTestStore(t)
	id := startedRun(t, s)
	at := DatabaseIdentity{
		Repository: "octo/app", Commit: "sha-one", SourceFilter: "f0", Mode: AnalysisModeRegular,
	}
	q := QueryRun{
		QueryID: "actions/unpinned-tag", QueryVersion: "u1", CLIVersion: "2.27.1",
		Repository: "octo/app", DBCommit: "sha-one", SourceFilter: "f0",
		AnalysisMode: AnalysisModeRegular,
	}

	ledger, err := s.RecordQueryRun(q)
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if _, _, found, err := s.AnsweredBy(q); err != nil || found {
		t.Fatalf("a ledger row no member finished storing answered (found=%v, %v)", found, err)
	}

	if err := s.RecordAnalysisMemberResult(id, at, MemberResult{
		Status: AnalysisSucceeded, ResultPath: "/work/run-1/octo_app.sarif", Results: 2, Counted: true,
		ByQuery: map[string]int{"actions/unpinned-tag": 2}, QueryRunID: ledger,
	}); err != nil {
		t.Fatalf("RecordAnalysisMemberResult: %v", err)
	}
	from, got, found, err := s.AnsweredBy(q)
	if err != nil || !found {
		t.Fatalf("the stored evaluation did not answer its own key (found=%v, %v)", found, err)
	}
	if from != id || got.QueryRunID != ledger || got.ResultPath != "/work/run-1/octo_app.sarif" {
		t.Errorf("answered by run %d, row %d, %q; want run %d, row %d and its document",
			from, got.QueryRunID, got.ResultPath, id, ledger)
	}
	if !got.Counted || got.Results != 2 || got.ByQuery["actions/unpinned-tag"] != 2 {
		t.Errorf("the answer carries counted=%v results=%d %v, want what the member counted",
			got.Counted, got.Results, got.ByQuery)
	}

	elsewhere := q
	elsewhere.DBCommit = "sha-other"
	if _, _, found, _ := s.AnsweredBy(elsewhere); found {
		t.Error("an evaluation at another commit answered")
	}
}

// A member's results are stored under the database it was built at, so a member left at the
// identity the resolution guessed would attribute them to a database that does not exist.
func TestAMemberMovesToTheDatabaseItWasBuiltAt(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertRepository("octo/unanalyzed", ""); err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	id := startedRun(t, s)
	from := DatabaseIdentity{Repository: "octo/unanalyzed", Mode: AnalysisModeRegular}
	to := DatabaseIdentity{
		Repository: "octo/unanalyzed", Commit: "sha-built", SourceFilter: "f1", Mode: AnalysisModeRegular,
	}
	if err := s.SetAnalysisMemberDatabase(id, from, to, "/cache/octo/unanalyzed/db"); err != nil {
		t.Fatalf("SetAnalysisMemberDatabase: %v", err)
	}
	if err := s.RecordAnalysisMemberResult(id, to, MemberResult{
		Status: AnalysisSucceeded, Results: 1, Counted: true,
		ByQuery: map[string]int{"actions/unpinned-tag": 1},
	}); err != nil {
		t.Fatalf("RecordAnalysisMemberResult: %v", err)
	}

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if len(run.Members) != 3 {
		t.Errorf("moving a member left %d members, want 3", len(run.Members))
	}
	m := memberNamed(t, run, "octo/unanalyzed")
	if m.DatabaseIdentity != to || m.DatabasePath != "/cache/octo/unanalyzed/db" {
		t.Errorf("the member is at %+v in %q, want %+v where it was built", m.DatabaseIdentity,
			m.DatabasePath, to)
	}
	if m.Status != AnalysisSucceeded || m.ByQuery["actions/unpinned-tag"] != 1 {
		t.Errorf("the moved member reads %q with %v, want what was recorded against it", m.Status, m.ByQuery)
	}

	other := DatabaseIdentity{Repository: "octo/other", Mode: AnalysisModeRegular}
	if err := s.SetAnalysisMemberDatabase(id, to, other, ""); err == nil {
		t.Error("a member became another repository")
	}
	if err := s.SetAnalysisMemberDatabase(id, from, to, ""); err == nil {
		t.Error("a member was moved from an identity it no longer holds")
	}
}

func runRequest(s *Store) AnalysisRequest {
	return AnalysisRequest{
		RequestedBy: "operator",
		Predicate:   "rules=actions%2F*",
		Mode:        AnalysisModeRegular,
		Queries: []AnalysisQuery{
			{QueryID: "actions/unpinned-tag", QueryVersion: "u1"},
		},
		Databases: []ScopedDatabase{
			{DatabaseIdentity: DatabaseIdentity{
				Repository: "octo/app", Commit: "sha-one",
				SourceFilter: "f0", Mode: AnalysisModeRegular,
			}, Status: DatabaseRetained, Path: "/cache/octo/app/db"},
			{DatabaseIdentity: DatabaseIdentity{
				Repository: "octo/tool", Commit: "sha-two",
				SourceFilter: "f0", Mode: AnalysisModeRegular,
			}, Status: DatabaseBuildable},
		},
		Gaps: []DatabaseGap{{Repository: "octo/unanalyzed", Reason: "no regular analysis on record"}},
	}
}

func startedRun(t *testing.T, s *Store) int64 {
	t.Helper()
	id, err := s.CreateAnalysisRun(runRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	return id
}

// A run that kept only the databases it could analyze would quietly mean something narrower than
// it was asked for, and the narrowing would be invisible exactly where it matters most.
func TestARunKeepsEveryRepositoryTheScopeNamedIncludingTheOnesItCannotAnalyze(t *testing.T) {
	s := newTestStore(t)
	run, err := s.AnalysisRun(startedRun(t, s))
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if len(run.Members) != 3 {
		t.Fatalf("the run holds %d members, want one per repository the scope named", len(run.Members))
	}
	by := map[string]AnalysisMember{}
	for _, m := range run.Members {
		by[m.Repository] = m
	}
	if by["octo/unanalyzed"].Status != AnalysisNoDatabase {
		t.Errorf("a repository with no database is %q, want %q",
			by["octo/unanalyzed"].Status, AnalysisNoDatabase)
	}
	if by["octo/unanalyzed"].Detail == "" {
		t.Error("a member that cannot be analyzed does not say why")
	}
	if by["octo/app"].Status != AnalysisPending {
		t.Errorf("a repository with a database is %q, want %q",
			by["octo/app"].Status, AnalysisPending)
	}
}

// What a run was asked for and what it resolved to are different facts. Keeping only the second
// makes a run unable to say what question it was answering.
func TestARunRecordsThePredicateItWasAskedFor(t *testing.T) {
	s := newTestStore(t)
	run, err := s.AnalysisRun(startedRun(t, s))
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if run.Predicate != "rules=actions%2F*" {
		t.Errorf("the run records predicate %q, want the one it was asked for", run.Predicate)
	}
	if run.RequestedBy != "operator" {
		t.Errorf("the run is attributed to %q, want the person who asked for it", run.RequestedBy)
	}
	if len(run.Queries) != 1 || run.Queries[0].QueryID != "actions/unpinned-tag" {
		t.Errorf("the run names %v, want the explicit query list it was given", run.Queries)
	}
}

// A run with no query would analyze nothing while looking like work, and a run with no database
// would report a clean result for a scope nothing ran against.
func TestARunWithNothingToRunOrNothingToRunAgainstIsRefused(t *testing.T) {
	s := newTestStore(t)

	empty := runRequest(s)
	empty.Queries = nil
	if _, err := s.CreateAnalysisRun(empty); err == nil {
		t.Error("a run naming no query was accepted")
	}

	nothing := runRequest(s)
	nothing.Databases, nothing.Gaps = nil, nil
	if _, err := s.CreateAnalysisRun(nothing); err == nil {
		t.Error("a run over no repositories at all was accepted")
	}
}

// A run nobody signed could not be told from one the tool started itself.
func TestARunWithNobodyNamedIsRefused(t *testing.T) {
	s := newTestStore(t)
	unsigned := runRequest(s)
	unsigned.RequestedBy = ""
	if _, err := s.CreateAnalysisRun(unsigned); err == nil {
		t.Error("an unsigned run was accepted")
	}
}

// Progress is per member, so a status change moves the member rather than adding a second row for
// the same database.
func TestAdvancingAMemberMovesItRatherThanAddingASecond(t *testing.T) {
	s := newTestStore(t)
	id := startedRun(t, s)
	target := DatabaseIdentity{
		Repository: "octo/app", Commit: "sha-one", SourceFilter: "f0", Mode: AnalysisModeRegular,
	}

	for _, status := range []string{AnalysisRunning, AnalysisSucceeded} {
		if err := s.SetAnalysisMemberStatus(id, target, status, ""); err != nil {
			t.Fatalf("SetAnalysisMemberStatus(%s): %v", status, err)
		}
	}

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if len(run.Members) != 3 {
		t.Errorf("advancing a member left %d members, want 3", len(run.Members))
	}
	for _, m := range run.Members {
		if m.Repository == "octo/app" && m.Status != AnalysisSucceeded {
			t.Errorf("the advanced member is %q, want %q", m.Status, AnalysisSucceeded)
		}
	}
}

// A failure has to say what failed. A member that is only "failed" tells a reader a repository
// produced nothing and nothing about whether that is worth acting on.
func TestAFailedMemberCarriesWhatWentWrong(t *testing.T) {
	s := newTestStore(t)
	id := startedRun(t, s)
	target := DatabaseIdentity{
		Repository: "octo/app", Commit: "sha-one", SourceFilter: "f0", Mode: AnalysisModeRegular,
	}
	if err := s.SetAnalysisMemberStatus(id, target, AnalysisFailed, "exit status 32"); err != nil {
		t.Fatalf("SetAnalysisMemberStatus: %v", err)
	}
	run, _ := s.AnalysisRun(id)
	for _, m := range run.Members {
		if m.Repository == "octo/app" && m.Detail != "exit status 32" {
			t.Errorf("the failed member reports %q, want what went wrong", m.Detail)
		}
	}
}

// A member that found nothing and a member that has not run both show no findings. Recording the
// count as a plain integer makes the second indistinguishable from the first, which is the
// absent-is-not-zero failure arriving where somebody is about to call a query clean.
func TestAMemberThatFoundNothingIsNotTheSameAsOneThatHasNotRun(t *testing.T) {
	s := newTestStore(t)
	id := startedRun(t, s)
	target := DatabaseIdentity{
		Repository: "octo/app", Commit: "sha-one", SourceFilter: "f0", Mode: AnalysisModeRegular,
	}
	if err := s.RecordAnalysisMemberResult(id, target, MemberResult{
		Status: AnalysisSucceeded, ResultPath: "/work/run-1/octo_app.sarif", Results: 0, Counted: true,
	}); err != nil {
		t.Fatalf("RecordAnalysisMemberResult: %v", err)
	}

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	by := map[string]AnalysisMember{}
	for _, m := range run.Members {
		by[m.Repository] = m
	}
	if !by["octo/app"].Counted || by["octo/app"].Results != 0 {
		t.Errorf("an analyzed member reports counted=%v results=%d, want a counted zero",
			by["octo/app"].Counted, by["octo/app"].Results)
	}
	if by["octo/unanalyzed"].Counted {
		t.Error("a member nothing ran against reports a count, which reads as a clean result")
	}
}

// The run's own total is what a reader compares two runs of a query by, and summing it on the
// client would count the members a page happens to be showing.
func TestARunTotalsTheFindingsItsMembersReported(t *testing.T) {
	s := newTestStore(t)
	id := startedRun(t, s)
	for repo, commit := range map[string]string{"octo/app": "sha-one", "octo/tool": "sha-two"} {
		if err := s.RecordAnalysisMemberResult(id, DatabaseIdentity{
			Repository: repo, Commit: commit, SourceFilter: "f0", Mode: AnalysisModeRegular,
		}, MemberResult{Status: AnalysisSucceeded, Results: 3, Counted: true}); err != nil {
			t.Fatalf("RecordAnalysisMemberResult(%s): %v", repo, err)
		}
	}

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if run.Results != 6 {
		t.Errorf("the run totals %d findings, want 6", run.Results)
	}

	runs, err := s.AnalysisRuns()
	if err != nil {
		t.Fatalf("AnalysisRuns: %v", err)
	}
	if runs[0].Results != 6 {
		t.Errorf("the listed run totals %d findings, want 6", runs[0].Results)
	}
}

// The tally is what a reader decides on, and it must account for every member: a count that
// reports only successes is the absent-is-not-zero failure wearing a progress bar.
func TestTheTallyAccountsForEveryMember(t *testing.T) {
	s := newTestStore(t)
	id := startedRun(t, s)
	if err := s.SetAnalysisMemberStatus(id, DatabaseIdentity{
		Repository: "octo/app", Commit: "sha-one", SourceFilter: "f0", Mode: AnalysisModeRegular,
	}, AnalysisSucceeded, ""); err != nil {
		t.Fatalf("SetAnalysisMemberStatus: %v", err)
	}

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	total := 0
	for _, n := range run.Tally {
		total += n
	}
	if total != len(run.Members) {
		t.Errorf("the tally accounts for %d of %d members", total, len(run.Members))
	}
	if run.Tally[AnalysisSucceeded] != 1 || run.Tally[AnalysisNoDatabase] != 1 {
		t.Errorf("the tally is %v, want one succeeded and one with no database", run.Tally)
	}
}

// A run is listed newest first, because the one somebody is waiting on is the one they just
// started.
func TestRunsAreListedNewestFirst(t *testing.T) {
	s := newTestStore(t)
	first := startedRun(t, s)
	second := startedRun(t, s)

	runs, err := s.AnalysisRuns()
	if err != nil {
		t.Fatalf("AnalysisRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("listed %d runs, want 2", len(runs))
	}
	if runs[0].RowID != second || runs[1].RowID != first {
		t.Errorf("runs listed as %d,%d, want the newest first", runs[0].RowID, runs[1].RowID)
	}
}

// The list says what each run was asked to run. Without it a reader can only tell runs apart by
// their predicate, and every run of the same scope looks like the same run.
func TestAListedRunNamesTheVersionItWasAskedFor(t *testing.T) {
	s := newTestStore(t)
	id := startedRun(t, s)

	runs, err := s.AnalysisRuns()
	if err != nil {
		t.Fatalf("AnalysisRuns: %v", err)
	}
	listed := runs[0]
	if listed.RowID != id {
		t.Fatalf("listed run %d, want %d", listed.RowID, id)
	}

	full, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if len(listed.Queries) != len(full.Queries) || len(listed.Queries) == 0 {
		t.Fatalf("the list carries %d queries and the run carries %d",
			len(listed.Queries), len(full.Queries))
	}
	for i, q := range listed.Queries {
		if q != full.Queries[i] {
			t.Errorf("listed query %d is %v, want %v", i, q, full.Queries[i])
		}
	}
}
