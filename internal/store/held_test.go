package store

import (
	"strings"
	"testing"
)

func heldRequest(s *Store) AnalysisRequest {
	req := runRequest(s)
	req.Held = true
	return req
}

func heldAt() DatabaseIdentity {
	return DatabaseIdentity{
		Repository: "octo/app", Commit: "sha-one", SourceFilter: "f0", Mode: AnalysisModeRegular,
	}
}

func twoHeld() []Finding {
	return []Finding{
		{Provider: ProviderCodeQL, RuleID: "actions/unpinned-tag", FilePath: ".github/workflows/ci.yml",
			StartLine: 12, StartColumn: 15, EndLine: 12, EndColumn: 40,
			Message: "Unpinned 3rd party Action", Severity: "warning", SecuritySeverity: score(5.5),
			Fingerprint: "fp-a", ResultHash: "rh-a"},
		{Provider: ProviderCodeQL, RuleID: "actions/other", FilePath: ".github/workflows/ci.yml",
			StartLine: 30, StartColumn: 9, Message: "Something else", Severity: "note",
			Fingerprint: "fp-b", ResultHash: "rh-b"},
	}
}

// heldEvaluation is the ledger row a held member's results were read under.
func heldEvaluation(t *testing.T, s *Store) int64 {
	t.Helper()
	id, err := s.RecordQueryRun(QueryRun{
		QueryID: "actions/unpinned-tag", QueryVersion: "u1", CLIVersion: "2.27.1",
		Repository: "octo/app", DBCommit: "sha-one", SourceFilter: "f0",
		AnalysisMode: AnalysisModeRegular, RecordedBy: RecordedByRun, Held: true,
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	return id
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

// Holding is a property of the run, not of its shape: refining a query against a closure database
// with findings off is legitimate, and only the comparison with alerts needs a regular run.
func TestARunWithFindingsOffReadsAsHeldOverAnyShape(t *testing.T) {
	s := newTestStore(t)

	extended := heldRequest(s)
	extended.Mode = AnalysisModeExtended
	if _, err := s.CreateAnalysisRun(extended); err != nil {
		t.Errorf("a held run over extended databases was refused: %v", err)
	}

	id, err := s.CreateAnalysisRun(heldRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if !run.Held {
		t.Error("a run asked for with findings off does not read as held")
	}
	listed, err := s.AnalysisRuns()
	if err != nil {
		t.Fatalf("AnalysisRuns: %v", err)
	}
	for _, r := range listed {
		if !r.Held {
			t.Errorf("the run list reads run %d as storing findings; both runs hold them", r.RowID)
		}
	}

	plain, err := s.AnalysisRun(startedRun(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Held {
		t.Error("a run nobody asked to hold reads as held")
	}
}

// A held member keeps its counts, its list and the evaluation that produced them, and writes no
// finding and no sighting that every other surface would count.
func TestAHeldMemberKeepsItsCountsItsListAndItsEvaluationAndStoresNoFinding(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateAnalysisRun(heldRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	evaluation := heldEvaluation(t, s)

	if err := s.RecordHeldMemberResult(id, heldAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 2, Counted: true, QueryRunID: evaluation,
		ByQuery: map[string]int{"actions/unpinned-tag": 1, "actions/other": 1},
	}, twoHeld()); err != nil {
		t.Fatalf("RecordHeldMemberResult: %v", err)
	}

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatal(err)
	}
	m := memberNamed(t, run, "octo/app")
	if !m.Counted || m.Results != 2 || m.ByQuery["actions/unpinned-tag"] != 1 {
		t.Errorf("the member reads counted=%v %d %v, want what the run counted",
			m.Counted, m.Results, m.ByQuery)
	}

	listed, err := s.RunFindings(id, "octo/app", "", 100)
	if err != nil {
		t.Fatalf("RunFindings: %v", err)
	}
	if !listed.Held || !listed.Evaluated || len(listed.Findings) != 2 {
		t.Fatalf("the member lists %+v, want the 2 held results, marked as held", listed)
	}
	first := listed.Findings[0]
	if first.RuleID != "actions/unpinned-tag" || first.FilePath != ".github/workflows/ci.yml" ||
		first.StartLine != 12 || first.StartColumn != 15 || first.Message != "Unpinned 3rd party Action" {
		t.Errorf("the first held result reads %+v", first)
	}
	if first.FindingRowID != 0 {
		t.Errorf("a held result names finding %d, and no finding exists", first.FindingRowID)
	}
	one, err := s.RunFindings(id, "octo/app", "actions/other", 100)
	if err != nil || len(one.Findings) != 1 || one.Findings[0].RuleID != "actions/other" {
		t.Errorf("narrowed to one query the member lists %+v (%v)", one.Findings, err)
	}

	for _, table := range []string{"abom_finding", "abom_finding_sighting", "repository"} {
		if n := countRows(t, s, table); n != 0 {
			t.Errorf("a held run left %d rows in %s", n, table)
		}
	}
	var held int
	if err := s.DB().QueryRow(`SELECT held FROM abom_query_run WHERE row_id = ?`, evaluation).
		Scan(&held); err != nil || held != 1 {
		t.Errorf("the evaluation reads held=%d (%v), want it marked as the held one", held, err)
	}
}

// Holding again replaces what the member held rather than adding to it, or a second pass would
// double every count and list each result twice.
func TestHoldingAgainReplacesWhatTheMemberHeld(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateAnalysisRun(heldRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	for _, held := range [][]Finding{twoHeld(), twoHeld()[:1]} {
		if err := s.RecordHeldMemberResult(id, heldAt(), MemberResult{
			Status: AnalysisSucceeded, Results: len(held), Counted: true,
			ByQuery: map[string]int{"actions/unpinned-tag": 1},
		}, held); err != nil {
			t.Fatalf("RecordHeldMemberResult: %v", err)
		}
	}
	listed, err := s.RunFindings(id, "octo/app", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Findings) != 1 {
		t.Errorf("after holding again the member lists %d results, want the 1 the last pass found",
			len(listed.Findings))
	}
}

// Each kind of run links only its own kind of evaluation. A findings run linked to a held one
// would list sightings that were never written; a held run linked to a full one would let the next
// run skip on an answer whose findings are held.
func TestARunLinksOnlyTheKindOfEvaluationItsFindingsSettingProduces(t *testing.T) {
	s := newTestStore(t)
	held, err := s.CreateAnalysisRun(heldRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	full, err := s.RecordQueryRun(QueryRun{
		QueryID: "actions/unpinned-tag", QueryVersion: "u1", CLIVersion: "2.27.1",
		Repository: "octo/app", DBCommit: "sha-one", SourceFilter: "f0",
		AnalysisMode: AnalysisModeRegular,
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if err := s.RecordHeldMemberResult(held, heldAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 2, Counted: true, QueryRunID: full,
	}, twoHeld()); err == nil {
		t.Error("a held member was linked to an evaluation whose findings are stored")
	}

	run := startedRun(t, s)
	if err := s.RecordAnalysisMemberResult(run, heldAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 2, Counted: true, QueryRunID: heldEvaluation(t, s),
	}); err == nil {
		t.Error("a findings run's member was linked to a held evaluation")
	}
	if err := s.RecordHeldMemberResult(run, heldAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 2, Counted: true,
	}, twoHeld()); err == nil {
		t.Error("a run that stores findings held results beside them")
	}
	if n := countRows(t, s, "abom_held_result"); n != 0 {
		t.Errorf("a refused write left %d held results", n)
	}
}

// A held evaluation is on the ledger because it happened, and it answers no skip: the crawl's
// lookups by key and by content, and a run's, all treat it as absent.
func TestAHeldEvaluationAnswersNoSkip(t *testing.T) {
	s := newTestStore(t)
	q := QueryRun{
		QueryID: "actions/unpinned-tag", QueryVersion: "u1", CLIVersion: "2.27.1",
		Repository: "octo/app", DBCommit: "sha-one", SourceFilter: "f0",
		AnalysisMode: AnalysisModeRegular,
	}
	asHeld := q
	asHeld.Held = true
	evaluation, err := s.RecordQueryRun(asHeld)
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	id, err := s.CreateAnalysisRun(heldRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	if err := s.RecordHeldMemberResult(id, heldAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 2, Counted: true, QueryRunID: evaluation,
	}, twoHeld()); err != nil {
		t.Fatalf("RecordHeldMemberResult: %v", err)
	}
	if err := s.RecordCommitContent("octo/app", "sha-one", "digest-1"); err != nil {
		t.Fatalf("RecordCommitContent: %v", err)
	}

	if found, err := s.QueryRunExists(q); err != nil || found {
		t.Errorf("QueryRunExists = %v (%v), want a held evaluation to answer nothing", found, err)
	}
	if found, err := s.QueryRunExistsForContent(q, "digest-1"); err != nil || found {
		t.Errorf("QueryRunExistsForContent = %v (%v), want a held evaluation to answer nothing", found, err)
	}
	if _, _, found, err := s.AnsweredBy(q); err != nil || found {
		t.Errorf("AnsweredBy found=%v (%v), want a held member to answer no run", found, err)
	}

	stored, err := s.RecordQueryRun(q)
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if stored == evaluation {
		t.Error("the evaluation that stores findings collapsed into the held one")
	}
}

// Somebody converting part of what a run held puts sightings under a held evaluation. Every result
// left held has not stopped being reported, so the sweep must not read that evaluation as the
// newest word on the repository.
func TestAHeldEvaluationNeverMakesAFindingStale(t *testing.T) {
	s := newTestStore(t)
	repoID, crawled := sightingFixture(t, s, "octo-org/app", "cafe1234")
	if _, err := s.AddFindings([]Finding{{
		Provider: ProviderCodeQL, QueryRunID: crawled, RuleID: "actions/unpinned-tag",
		RepositoryID: repoID, FilePath: ".github/workflows/ci.yml", StartLine: 1,
		Fingerprint: "still-reported",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	held, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "actions/unpinned-tag", QueryVersion: "2.0.0",
		Pack: "codeql/actions-queries", PackVersion: "0.6.35",
		Repository: "octo-org/app", DBCommit: "cafe1234", Held: true,
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if _, err := s.AddFindings([]Finding{{
		Provider: ProviderCodeQL, QueryRunID: held, RuleID: "actions/unpinned-tag",
		RepositoryID: repoID, FilePath: ".github/workflows/ci.yml", StartLine: 9,
		Fingerprint: "converted",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	report, err := s.StaleFindings()
	if err != nil {
		t.Fatalf("StaleFindings: %v", err)
	}
	for _, f := range report.Stale {
		t.Errorf("finding %q reads as stale because part of a held run was converted", f.Fingerprint)
	}
}

// A predicting-era store migrates runs and results to held names in place.
func TestSQLiteStoreThatPredictedIsMigratedToHeldResultsInPlace(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateAnalysisRun(heldRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	if err := s.RecordHeldMemberResult(id, heldAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 2, Counted: true,
	}, twoHeld()); err != nil {
		t.Fatalf("RecordHeldMemberResult: %v", err)
	}
	// Recreate the predicting-era shape.
	for _, stmt := range []string{
		`ALTER TABLE abom_analysis_run RENAME COLUMN held TO prediction`,
		`DROP INDEX IF EXISTS idx_held_member`,
		`ALTER TABLE abom_held_result DROP COLUMN provider`,
		`ALTER TABLE abom_held_result DROP COLUMN security_severity`,
		`ALTER TABLE abom_held_result RENAME TO abom_predicted_result`,
		`CREATE INDEX idx_predicted_member ON abom_predicted_result(run_id, repository, rule_id)`,
	} {
		if _, err := s.DB().Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatal(err)
	}
	if !run.Held {
		t.Error("a run that predicted no longer reads as held")
	}
	if n := countRows(t, s, "abom_held_result"); n != 2 {
		t.Errorf("abom_held_result holds %d rows, want the 2 the run held", n)
	}
	for _, gone := range []string{"abom_predicted_result", "idx_predicted_member"} {
		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, gone).
			Scan(&n); err != nil || n != 0 {
			t.Errorf("%s is still in the schema (%d, %v)", gone, n, err)
		}
	}
	if has, err := s.hasColumn("abom_analysis_run", "prediction"); err != nil || has {
		t.Errorf("abom_analysis_run still has a prediction column (%v)", err)
	}
	if err := s.Migrate(); err != nil {
		t.Errorf("migrating a second time: %v", err)
	}
}

// heldPickedRun is a held run of acme/a over three repositories: one where it reported a result
// (and acme/b one beside it), one where it ran and reported nothing, and one nothing ran against.
func heldPickedRun(t *testing.T, s *Store) (runID, reported, other int64) {
	t.Helper()
	at := func(name string) ScopedDatabase {
		return ScopedDatabase{DatabaseIdentity: DatabaseIdentity{
			Repository: name, Commit: "c1", Mode: AnalysisModeRegular,
		}, Status: DatabaseRetained, Path: "/db/" + name}
	}
	runID, err := s.CreateAnalysisRun(AnalysisRequest{
		RequestedBy: "operator", Held: true,
		Queries:   []AnalysisQuery{{QueryID: "acme/a", QueryVersion: "v1"}, {QueryID: "acme/b", QueryVersion: "v1"}},
		Databases: []ScopedDatabase{at("octo/app"), at("octo/quiet")},
		Gaps:      []DatabaseGap{{Repository: "octo/absent", Reason: "never analyzed"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"octo/app", "octo/quiet"} {
		var held []Finding
		if name == "octo/app" {
			held = []Finding{
				{RuleID: "acme/a", FilePath: ".github/workflows/ci.yml", StartLine: 6, Fingerprint: "a"},
				{RuleID: "acme/b", FilePath: ".github/workflows/ci.yml", StartLine: 9, Fingerprint: "b"},
			}
		}
		if err := s.RecordHeldMemberResult(runID, at(name).DatabaseIdentity, MemberResult{
			Status: AnalysisSucceeded, Counted: true, Results: len(held),
		}, held); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := s.RunFindings(runID, "octo/app", "", 10)
	if err != nil || len(listed.Findings) != 2 {
		t.Fatalf("the fixture run lists %+v (err %v)", listed, err)
	}
	for _, f := range listed.Findings {
		switch f.RuleID {
		case "acme/a":
			reported = f.HeldResultID
		case "acme/b":
			other = f.HeldResultID
		}
	}
	if reported == 0 || other == 0 {
		t.Fatalf("the held results name no row: %+v", listed.Findings)
	}
	return runID, reported, other
}

// Refining a query happens with findings off, so what a run held is where a false alarm is
// picked from, and a repository it held nothing in is where a miss is.
func TestWhatARunHeldIsPickedAsEvidence(t *testing.T) {
	s := newTestStore(t)
	runID, reported, _ := heldPickedRun(t, s)

	want := []RequestEvidence{
		{RunID: runID, Repository: "octo/app", HeldResultID: reported, Kind: RequestFalseAlarm},
		{RunID: runID, Repository: "octo/quiet", Kind: RequestMissed},
	}
	if _, err := s.SaveQueryRequest(pickedRequest(runID, want...)); err != nil {
		t.Fatalf("SaveQueryRequest: %v", err)
	}
	got, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Evidence) != 2 || got[0].Evidence[0] != want[0] ||
		got[0].Evidence[1] != want[1] {
		t.Errorf("filed with %+v, read back %+v", want, got)
	}
	if err := s.CheckPicked(runID, "acme/a", want); err != nil {
		t.Errorf("a draft's picks from what the run held were refused: %v", err)
	}
}

// Two false alarms held in one repository are two pieces of evidence.
func TestTwoHeldResultsInOneRepositoryAreBothKept(t *testing.T) {
	s := newTestStore(t)
	runID, reported, other := heldPickedRun(t, s)
	if _, err := s.db.Exec(`UPDATE abom_held_result SET rule_id = 'acme/a' WHERE row_id = ?`, other); err != nil {
		t.Fatal(err)
	}
	r := pickedRequest(runID,
		RequestEvidence{RunID: runID, Repository: "octo/app", HeldResultID: reported, Kind: RequestFalseAlarm},
		RequestEvidence{RunID: runID, Repository: "octo/app", HeldResultID: other, Kind: RequestFalseAlarm})
	if _, err := s.SaveQueryRequest(r); err != nil {
		t.Fatalf("SaveQueryRequest: %v", err)
	}
	got, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Evidence) != 2 {
		t.Errorf("two held false alarms in one repository read back as %+v", got)
	}
}

// Evidence from what a run held is what that run held, for the query the request is about, and a
// run of one kind cannot be cited with the other kind's reference.
func TestHeldEvidenceIsOnlyWhatTheRunHeld(t *testing.T) {
	s := newTestStore(t)
	runID, reported, other := heldPickedRun(t, s)
	full, fired := pickedRun(t, s)

	for name, r := range map[string]QueryRequest{
		"a result another query reported": pickedRequest(runID,
			RequestEvidence{RunID: runID, Repository: "octo/app", HeldResultID: other, Kind: RequestFalseAlarm}),
		"a result held in another repository": pickedRequest(runID,
			RequestEvidence{RunID: runID, Repository: "octo/quiet", HeldResultID: reported, Kind: RequestFalseAlarm}),
		"a miss where nothing ran": pickedRequest(runID,
			RequestEvidence{RunID: runID, Repository: "octo/absent", Kind: RequestMissed}),
		"a finding cited from a held run": pickedRequest(runID,
			RequestEvidence{RunID: runID, Repository: "octo/app", FindingRowID: fired, Kind: RequestFalseAlarm}),
		"a held result cited from a findings run": pickedRequest(full,
			RequestEvidence{RunID: full, Repository: "octo/app", HeldResultID: reported, Kind: RequestFalseAlarm}),
		"a false alarm naming both": pickedRequest(runID,
			RequestEvidence{RunID: runID, Repository: "octo/app", FindingRowID: fired, HeldResultID: reported,
				Kind: RequestFalseAlarm}),
		"a miss naming a held result": pickedRequest(runID,
			RequestEvidence{RunID: runID, Repository: "octo/app", HeldResultID: reported, Kind: RequestMissed}),
	} {
		if _, err := s.SaveQueryRequest(r); err == nil {
			t.Errorf("%s was kept", name)
		}
	}
	if got, _ := s.QueryRequests(); len(got) != 0 {
		t.Errorf("refused requests left %d rows", len(got))
	}
	// Crossing kinds is refused for the kind, not as a result that happens not to exist.
	if _, err := s.SaveQueryRequest(pickedRequest(runID, RequestEvidence{RunID: runID,
		Repository: "octo/app", FindingRowID: fired, Kind: RequestFalseAlarm})); err == nil ||
		!strings.Contains(err.Error(), "held what it found") {
		t.Errorf("a finding cited from a held run answered %v, want the reason", err)
	}
	if _, err := s.SaveQueryRequest(pickedRequest(full, RequestEvidence{RunID: full,
		Repository: "octo/app", HeldResultID: reported, Kind: RequestFalseAlarm})); err == nil ||
		!strings.Contains(err.Error(), "stored what it found as findings") {
		t.Errorf("a held result cited from a findings run answered %v, want the reason", err)
	}
}

// Migrated evidence keeps what was filed and can then hold two picks in one repository.
func TestSQLiteEvidenceKeptBeforeHeldResultsIsMigratedInPlace(t *testing.T) {
	s := newTestStore(t)
	runID, fired := pickedRun(t, s)
	if _, err := s.SaveQueryRequest(pickedRequest(runID,
		RequestEvidence{RunID: runID, Repository: "octo/app", FindingRowID: fired, Kind: RequestFalseAlarm})); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE evidence_old (
		  request_id INTEGER NOT NULL, run_id INTEGER NOT NULL, repository TEXT NOT NULL,
		  finding_row_id INTEGER NOT NULL DEFAULT 0, kind TEXT NOT NULL,
		  PRIMARY KEY (request_id, run_id, repository, finding_row_id))`,
		`INSERT INTO evidence_old SELECT request_id, run_id, repository, finding_row_id, kind
		 FROM abom_query_request_evidence`,
		`DROP TABLE abom_query_request_evidence`,
		`ALTER TABLE evidence_old RENAME TO abom_query_request_evidence`,
	} {
		if _, err := s.DB().Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	got, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Evidence) != 1 || got[0].Evidence[0].FindingRowID != fired {
		t.Errorf("the evidence filed before the migration reads %+v", got)
	}

	held, reported, other := heldPickedRun(t, s)
	if _, err := s.db.Exec(`UPDATE abom_held_result SET rule_id = 'acme/a' WHERE row_id = ?`, other); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveQueryRequest(pickedRequest(held,
		RequestEvidence{RunID: held, Repository: "octo/app", HeldResultID: reported, Kind: RequestFalseAlarm},
		RequestEvidence{RunID: held, Repository: "octo/app", HeldResultID: other, Kind: RequestFalseAlarm},
	)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.QueryRequests(); len(got) != 2 || len(got[0].Evidence) != 2 {
		t.Errorf("after the migration two held picks in one repository read back as %+v", got)
	}
}

// convertibleRun is a held run whose two members both kept their results under a held evaluation,
// in repositories ABOM tracks: octo/app held two, octo/tool held one.
func convertibleRun(t *testing.T, s *Store) (runID int64, app []RunFinding) {
	t.Helper()
	id, err := s.CreateAnalysisRun(heldRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	for _, m := range []struct {
		at   DatabaseIdentity
		held []Finding
	}{
		{heldAt(), twoHeld()},
		{DatabaseIdentity{Repository: "octo/tool", Commit: "sha-two", SourceFilter: "f0",
			Mode: AnalysisModeRegular}, twoHeld()[1:]},
	} {
		if _, err := s.UpsertRepository(m.at.Repository, ""); err != nil {
			t.Fatal(err)
		}
		evaluation, err := s.RecordQueryRun(QueryRun{
			QueryID: "actions/unpinned-tag", QueryVersion: "u1", CLIVersion: "2.27.1",
			Repository: m.at.Repository, DBCommit: m.at.Commit, SourceFilter: m.at.SourceFilter,
			AnalysisMode: AnalysisModeRegular, RecordedBy: RecordedByRun, Held: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RecordHeldMemberResult(id, m.at, MemberResult{
			Status: AnalysisSucceeded, Results: len(m.held), Counted: true, QueryRunID: evaluation,
		}, m.held); err != nil {
			t.Fatalf("RecordHeldMemberResult: %v", err)
		}
	}
	listed, err := s.RunFindings(id, "octo/app", "", 100)
	if err != nil || len(listed.Findings) != 2 {
		t.Fatalf("the fixture lists %+v (%v)", listed, err)
	}
	return id, listed.Findings
}

// Converting part of what a run held adds exactly those findings, attributed to the evaluation
// that reported them, and says who converted each one and from which run.
func TestConvertingPartOfAHeldRunAddsExactlyThoseFindings(t *testing.T) {
	s := newTestStore(t)
	id, app := convertibleRun(t, s)
	chosen := app[0]

	got, err := s.ConvertHeld(id, "octo/app", []int64{chosen.HeldResultID}, "operator")
	if err != nil {
		t.Fatalf("ConvertHeld: %v", err)
	}
	if got.Converted != 1 || got.Already != 0 {
		t.Errorf("the conversion reads %+v, want the 1 result chosen", got)
	}
	if n := countRows(t, s, "abom_finding"); n != 1 {
		t.Fatalf("abom_finding holds %d rows, want the 1 converted", n)
	}
	listed, err := s.RunFindings(id, "octo/app", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range listed.Findings {
		converted := f.FindingRowID != 0
		if converted != (f.HeldResultID == chosen.HeldResultID) {
			t.Errorf("held result %d reads converted=%v", f.HeldResultID, converted)
		}
		if converted && (f.ConvertedBy != "operator" || f.ConvertedAt == "") {
			t.Errorf("the converted result says it was converted by %q at %q", f.ConvertedBy, f.ConvertedAt)
		}
	}
	trace, err := s.FindingTrace(listed.Findings[0].FindingRowID)
	if err != nil {
		t.Fatalf("FindingTrace: %v", err)
	}
	if len(trace.Evaluations) != 1 || trace.Evaluations[0].Invoked != "actions/unpinned-tag" ||
		trace.Evaluations[0].CLIVersion != "2.27.1" || trace.Evaluations[0].RecordedBy != RecordedByRun ||
		len(trace.Evaluations[0].Runs) != 1 || trace.Evaluations[0].Runs[0].RunID != id {
		t.Errorf("the converted finding traces to %+v, want the held evaluation and the run behind it",
			trace.Evaluations)
	}
	var who string
	if err := s.DB().QueryRow(`SELECT converted_by FROM abom_held_conversion WHERE run_id = ?`, id).
		Scan(&who); err != nil || who != "operator" {
		t.Errorf("the conversion records %q (%v), want who converted it", who, err)
	}
}

// Converting what is already converted changes nothing, and says so.
func TestConvertingTwiceAddsNothing(t *testing.T) {
	s := newTestStore(t)
	id, app := convertibleRun(t, s)
	for range 2 {
		if _, err := s.ConvertHeld(id, "octo/app", []int64{app[0].HeldResultID}, "operator"); err != nil {
			t.Fatalf("ConvertHeld: %v", err)
		}
	}
	again, err := s.ConvertHeld(id, "octo/app", []int64{app[0].HeldResultID}, "someone-else")
	if err != nil {
		t.Fatalf("ConvertHeld: %v", err)
	}
	if again.Converted != 0 || again.Already != 1 {
		t.Errorf("converting again reads %+v, want nothing new and the 1 already converted", again)
	}
	if n := countRows(t, s, "abom_finding_sighting"); n != 1 {
		t.Errorf("converting again left %d sightings, want 1", n)
	}
}

// Naming no result converts the member, and naming no repository converts the run.
func TestConvertingAMemberOrAWholeRun(t *testing.T) {
	s := newTestStore(t)
	id, _ := convertibleRun(t, s)
	member, err := s.ConvertHeld(id, "octo/app", nil, "operator")
	if err != nil || member.Converted != 2 {
		t.Fatalf("converting octo/app reads %+v (%v), want its 2 results", member, err)
	}
	run, err := s.ConvertHeld(id, "", nil, "operator")
	if err != nil || run.Converted != 1 || run.Already != 2 {
		t.Errorf("converting the run reads %+v (%v), want octo/tool's 1 and the 2 already converted", run, err)
	}
	if n := countRows(t, s, "abom_finding"); n != 3 {
		t.Errorf("abom_finding holds %d rows, want the 3 the run held", n)
	}
}

// A conversion names a result the run held, in a run that held it, backed by an evaluation it can
// be traced to, by somebody who can be named.
func TestAConversionIsRefusedWhenItCannotBeTraced(t *testing.T) {
	s := newTestStore(t)
	id, app := convertibleRun(t, s)
	_, other := convertibleRun(t, s)

	for name, convert := range map[string]func() error{
		"a run that stored findings": func() error {
			_, err := s.ConvertHeld(startedRun(t, s), "", nil, "operator")
			return err
		},
		"a result another run held": func() error {
			_, err := s.ConvertHeld(id, "octo/app", []int64{other[0].HeldResultID}, "operator")
			return err
		},
		"a result held in another repository": func() error {
			_, err := s.ConvertHeld(id, "octo/tool", []int64{app[0].HeldResultID}, "operator")
			return err
		},
		"nobody named": func() error {
			_, err := s.ConvertHeld(id, "octo/app", nil, "")
			return err
		},
		"a repository the run does not cover": func() error {
			_, err := s.ConvertHeld(id, "octo/elsewhere", nil, "operator")
			return err
		},
	} {
		if err := convert(); err == nil {
			t.Errorf("converting %s was accepted", name)
		}
	}

	if _, err := s.db.Exec(`UPDATE abom_analysis_member SET query_run_id = NULL
		WHERE run_id = ? AND repository = 'octo/app'`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConvertHeld(id, "octo/app", nil, "operator"); err == nil ||
		!strings.Contains(err.Error(), "again") {
		t.Errorf("a member held with no evaluation on record answered %v, want it asked to run again", err)
	}
	if n := countRows(t, s, "abom_finding"); n != 0 {
		t.Errorf("refused conversions left %d findings", n)
	}
}

// Analyzing the member again replaces what it held, and what was converted from it still reads as
// converted: the finding is what was adopted, not the held row it came from.
func TestAConvertedResultStillReadsAsConvertedAfterTheMemberIsHeldAgain(t *testing.T) {
	s := newTestStore(t)
	id, app := convertibleRun(t, s)
	if _, err := s.ConvertHeld(id, "octo/app", []int64{app[0].HeldResultID}, "operator"); err != nil {
		t.Fatal(err)
	}
	var evaluation int64
	if err := s.DB().QueryRow(`SELECT query_run_id FROM abom_analysis_member
		WHERE run_id = ? AND repository = 'octo/app'`, id).Scan(&evaluation); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordHeldMemberResult(id, heldAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 2, Counted: true, QueryRunID: evaluation,
	}, twoHeld()); err != nil {
		t.Fatal(err)
	}
	listed, err := s.RunFindings(id, "octo/app", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var converted int
	for _, f := range listed.Findings {
		if f.FindingRowID != 0 {
			converted++
		}
	}
	if converted != 1 {
		t.Errorf("after holding again %d results read as converted, want the 1 that was", converted)
	}
}
