package store

import (
	"testing"
)

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
