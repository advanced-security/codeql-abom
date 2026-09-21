package store

import (
	"testing"
)

// A finding row is unique on a key that spans commits, so it can name exactly one run while
// having been reported by several. That is why "is this still reported" is not answerable from
// `abom_finding`: it is a missing relation, not a missing WHERE clause. Priced on the reference
// corpus, the rule that used the single run condemned 2,420 findings other references still
// report. See plans/db/finding-lifecycle.md.
func TestASightingRecordsWhichRunReportedAFinding(t *testing.T) {
	s := newTestStore(t)
	repoID, runID := sightingFixture(t, s, "octo-org/app", "cafe1234")

	if _, err := s.AddFindings([]Finding{{
		Provider: ProviderCodeQL, QueryRunID: runID, RuleID: "actions/unpinned-tag",
		RepositoryID: repoID, FilePath: ".github/workflows/ci.yml", StartLine: 1,
		Fingerprint: "f1",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	if got := sightingsOf(t, s, "f1"); len(got) != 1 || got[0] != runID {
		t.Errorf("sightings = %v, want exactly the run that reported it (%d)", got, runID)
	}
}

// The whole point of the relation: one row, many runs. A second analysis at a different commit
// adds a sighting rather than replacing the first, or the sweep is back to consulting one run.
func TestTheSameFindingSeenByASecondRunGainsASecondSighting(t *testing.T) {
	s := newTestStore(t)
	repoID, first := sightingFixture(t, s, "octo-org/app", "cafe1234")
	second := recordRun(t, s, "octo-org/app", "beef5678")

	finding := Finding{
		Provider: ProviderCodeQL, RuleID: "actions/unpinned-tag", RepositoryID: repoID,
		FilePath: ".github/workflows/ci.yml", StartLine: 1, Fingerprint: "f1",
	}
	finding.QueryRunID = first
	if _, err := s.AddFindings([]Finding{finding}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	finding.QueryRunID = second
	if _, err := s.AddFindings([]Finding{finding}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	got := sightingsOf(t, s, "f1")
	if len(got) != 2 {
		t.Fatalf("sightings = %v, want both runs: one row can be reported at many references", got)
	}
	// And re-reporting an unchanged repository must not accumulate duplicates.
	if _, err := s.AddFindings([]Finding{finding}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if again := sightingsOf(t, s, "f1"); len(again) != 2 {
		t.Errorf("sightings = %v after re-ingest, want 2", again)
	}
}

// A finding ingested with no run cannot be attributed, and inventing a sighting for it would put
// a claim in the relation the sweep then trusts. 2,759 corpus findings are in exactly this state.
func TestAFindingWithNoRunRecordsNoSighting(t *testing.T) {
	s := newTestStore(t)
	repoID, _ := sightingFixture(t, s, "octo-org/app", "cafe1234")

	if _, err := s.AddFindings([]Finding{{
		RuleID: "actions/unpinned-tag", RepositoryID: repoID,
		FilePath: ".github/workflows/ci.yml", StartLine: 1, Fingerprint: "f1",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	if got := sightingsOf(t, s, "f1"); len(got) != 0 {
		t.Errorf("sightings = %v, want none: nothing said which run reported it", got)
	}
}

// The guard that matters most. No sighting exists for any of the 11,370 findings already stored,
// so a sweep that read "no sighting" as "not reported" would condemn the entire table on its
// first run. Absence in a run that could not have reported it is not absence.
func TestACorpusWithNoSightingsReportsNothingStale(t *testing.T) {
	s := newTestStore(t)
	repoID, runID := sightingFixture(t, s, "octo-org/app", "cafe1234")

	// Written straight to the table, bypassing the sighting the ingest path would record: this
	// is exactly the shape of every finding stored before the relation existed.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_finding (query_run_id, rule_id, repository_row_id, file_path, start_line,
		                          fingerprint)
		VALUES (?, 'actions/unpinned-tag', ?, '.github/workflows/ci.yml', 1, 'f1')`,
		runID, repoID); err != nil {
		t.Fatalf("seeding a finding with no sighting: %v", err)
	}

	report, err := s.StaleFindings()
	if err != nil {
		t.Fatalf("StaleFindings: %v", err)
	}
	if len(report.Stale) != 0 {
		t.Errorf("%d findings condemned with no sightings on record", len(report.Stale))
	}
	if report.Sightings != 0 {
		t.Errorf("Sightings = %d, want 0", report.Sightings)
	}
}

// The failure this plan was rewritten around: a finding carried only by an older reference is
// still reported, and the first design condemned 2,420 of them.
func TestAFindingSightedOnlyByAnOlderReferenceIsNotStale(t *testing.T) {
	s := newTestStore(t)
	repoID, old := sightingFixture(t, s, "octo-org/app", "cafe1234")
	newer := recordRun(t, s, "octo-org/app", "beef5678")

	// Reported at the older commit and not at the newer one. Both commits are tracked.
	if _, err := s.AddFindings([]Finding{{
		Provider: ProviderCodeQL, QueryRunID: old, RuleID: "actions/unpinned-tag",
		RepositoryID: repoID, FilePath: ".github/workflows/ci.yml", StartLine: 1,
		Fingerprint: "old-only",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	// Something else at the newer commit, so the newer run has sighted something and is not
	// mistaken for a run that attributed nothing.
	if _, err := s.AddFindings([]Finding{{
		Provider: ProviderCodeQL, QueryRunID: newer, RuleID: "actions/unpinned-tag",
		RepositoryID: repoID, FilePath: ".github/workflows/release.yml", StartLine: 9,
		Fingerprint: "still-here",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	report, err := s.StaleFindings()
	if err != nil {
		t.Fatalf("StaleFindings: %v", err)
	}
	for _, f := range report.Stale {
		if f.Fingerprint == "old-only" {
			t.Error("a finding an older reference still reports was condemned by a newer run")
		}
	}
}

// Stale means no run of the owning suite, at any tracked commit, sighted it.
//
// The re-analysis has to carry a different pack: `run_key` hashes query, pack, digest, CLI,
// repository and commit together, so re-running an unchanged commit with an unchanged pack
// resolves to the *same* run row and is no new evidence at all. Staleness is therefore only ever
// observable across a change in run identity — which is what a pack or CLI upgrade is.
func TestAFindingNoTrackedRunSightedIsStale(t *testing.T) {
	s := newTestStore(t)
	repoID, first := sightingFixture(t, s, "octo-org/app", "cafe1234")

	if _, err := s.AddFindings([]Finding{{
		Provider: ProviderCodeQL, QueryRunID: first, RuleID: "actions/unpinned-tag",
		RepositoryID: repoID, FilePath: ".github/workflows/ci.yml", StartLine: 1,
		Fingerprint: "fixed",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	again := recordRunWithPack(t, s, "octo-org/app", "cafe1234", "0.6.36")
	if again == first {
		t.Fatal("the upgraded run collapsed into the original; the test proves nothing")
	}
	if _, err := s.AddFindings([]Finding{{
		Provider: ProviderCodeQL, QueryRunID: again, RuleID: "actions/unpinned-tag",
		RepositoryID: repoID, FilePath: ".github/workflows/ci.yml", StartLine: 42,
		Fingerprint: "something-else",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	report, err := s.StaleFindings()
	if err != nil {
		t.Fatalf("StaleFindings: %v", err)
	}
	var found bool
	for _, f := range report.Stale {
		if f.Fingerprint == "fixed" {
			found = true
		}
		if f.Fingerprint == "something-else" {
			t.Error("a finding the newest run reported was condemned")
		}
	}
	if !found {
		t.Errorf("the finding no tracked run reports was not condemned; stale = %+v", report.Stale)
	}
}

// A report that states a conclusion without stating how much it looked at is one nobody can
// judge. On a corpus mid-recrawl the answer is mostly "not enough yet".
func TestTheReportStatesHowManySightingsItDrewOn(t *testing.T) {
	s := newTestStore(t)
	repoID, runID := sightingFixture(t, s, "octo-org/app", "cafe1234")

	if _, err := s.AddFindings([]Finding{{
		Provider: ProviderCodeQL, QueryRunID: runID, RuleID: "actions/unpinned-tag",
		RepositoryID: repoID, FilePath: ".github/workflows/ci.yml", StartLine: 1,
		Fingerprint: "f1",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	report, err := s.StaleFindings()
	if err != nil {
		t.Fatalf("StaleFindings: %v", err)
	}
	if report.Sightings != 1 {
		t.Errorf("Sightings = %d, want 1", report.Sightings)
	}
	if report.RepositoriesWithSightings != 1 {
		t.Errorf("RepositoriesWithSightings = %d, want 1", report.RepositoriesWithSightings)
	}
	if report.FindingsWithoutSighting != 0 {
		t.Errorf("FindingsWithoutSighting = %d, want 0", report.FindingsWithoutSighting)
	}
}

// sightingFixture gives a repository, the owning suite binding, and one recorded run.
func sightingFixture(t *testing.T, s *Store, repository, commit string) (int64, int64) {
	t.Helper()
	repoID, err := s.UpsertRepository(repository, "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	if err := s.RecordSuiteContents(findingsSuite, []SuiteQuery{
		{QueryID: "actions/unpinned-tag", Pack: "codeql/actions-queries", PackVersion: "0.6.35"},
	}); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}
	return repoID, recordRun(t, s, repository, commit)
}

func recordRun(t *testing.T, s *Store, repository, commit string) int64 {
	t.Helper()
	return recordRunWithPack(t, s, repository, commit, "0.6.35")
}

func recordRunWithPack(t *testing.T, s *Store, repository, commit, packVersion string) int64 {
	t.Helper()
	id, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "actions/unpinned-tag", QueryVersion: "1.0.0",
		Pack: "codeql/actions-queries", PackVersion: packVersion,
		Repository: repository, DBCommit: commit,
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	return id
}

func sightingsOf(t *testing.T, s *Store, fingerprint string) []int64 {
	t.Helper()
	rows, err := s.DB().Query(`
		SELECT sg.query_run_id FROM abom_finding_sighting sg
		JOIN abom_finding f ON f.row_id = sg.finding_row_id
		WHERE f.fingerprint = ? ORDER BY sg.query_run_id`, fingerprint)
	if err != nil {
		t.Fatalf("reading sightings: %v", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning a sighting: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading sightings: %v", err)
	}
	return out
}

// Extended analysis cannot supersede regular evidence: it looks at a different database, so a
// finding it does not reproduce is not a finding that went away. A sweep that let the closure run
// at the same commit stand as the newest evidence would condemn the repository's own results.
func TestAnExtendedRunDoesNotCondemnAFindingRegularAnalysisReports(t *testing.T) {
	s := newTestStore(t)
	repoID, regular := sightingFixture(t, s, "octo-org/app", "cafe1234")
	extended, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeExtended,
		QueryID: "actions/unpinned-tag", QueryVersion: "1.0.0",
		Pack: "codeql/actions-queries", PackVersion: "0.6.35",
		Repository: "octo-org/app", DBCommit: "cafe1234",
	})
	if err != nil {
		t.Fatalf("recording the extended run: %v", err)
	}
	if extended == regular {
		t.Fatal("the extended run collapsed into the regular one; the test proves nothing")
	}

	if _, err := s.AddFindings([]Finding{{
		Provider: ProviderCodeQL, QueryRunID: regular, RuleID: "actions/unpinned-tag",
		RepositoryID: repoID, FilePath: ".github/workflows/ci.yml", StartLine: 1,
		Fingerprint: "own-checkout",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	// The closure reports something of its own, so the extended run has attributed something and
	// is not mistaken for a run that reported nothing.
	if _, err := s.AddFindings([]Finding{{
		Provider: ProviderCodeQL, QueryRunID: extended, RuleID: "actions/unpinned-tag",
		RepositoryID: repoID, FilePath: "closure/vendor/toolkit/action.yml", StartLine: 4,
		Fingerprint: "closure-only",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	report, err := s.StaleFindings()
	if err != nil {
		t.Fatalf("StaleFindings: %v", err)
	}
	for _, f := range report.Stale {
		if f.Fingerprint == "own-checkout" {
			t.Error("a finding the repository's own analysis still reports was condemned by a " +
				"run of the closure, which never looked at the same database")
		}
	}
	if report.Sightings != 1 {
		t.Errorf("Sightings = %d, want only the regular evidence this report is drawn from",
			report.Sightings)
	}
}
