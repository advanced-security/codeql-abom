package store

import (
	"strings"
	"testing"
)

func predictionRequest(s *Store) AnalysisRequest {
	req := runRequest(s)
	req.Prediction = true
	return req
}

func predictedAt() DatabaseIdentity {
	return DatabaseIdentity{
		Repository: "octo/app", Commit: "sha-one", SourceFilter: "f0", Mode: AnalysisModeRegular,
	}
}

func twoPredicted() []PredictedResult {
	return []PredictedResult{
		{RuleID: "actions/unpinned-tag", FilePath: ".github/workflows/ci.yml", StartLine: 12,
			StartColumn: 15, EndLine: 12, EndColumn: 40, Message: "Unpinned 3rd party Action",
			Severity: "warning", Fingerprint: "fp-a", ResultHash: "rh-a"},
		{RuleID: "actions/other", FilePath: ".github/workflows/ci.yml", StartLine: 30,
			StartColumn: 9, Message: "Something else", Severity: "note", Fingerprint: "fp-b",
			ResultHash: "rh-b"},
	}
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

// Default Setup builds one repository's own checkout and stages nothing, so an extended database
// would predict alerts that will never appear. Refused when the prediction is asked for, rather than
// made and labeled.
func TestAPredictionIsMadeFromRegularDatabasesOnly(t *testing.T) {
	s := newTestStore(t)

	extended := predictionRequest(s)
	extended.Mode = AnalysisModeExtended
	if _, err := s.CreateAnalysisRun(extended); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Errorf("an extended prediction was answered %v, want a refusal naming the regular shape", err)
	}

	id, err := s.CreateAnalysisRun(predictionRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if !run.Prediction {
		t.Error("a run asked for as a prediction does not read as one")
	}
	listed, err := s.AnalysisRuns()
	if err != nil {
		t.Fatalf("AnalysisRuns: %v", err)
	}
	if len(listed) != 1 || !listed[0].Prediction {
		t.Errorf("the run list reads %+v, want the one prediction marked as one", listed)
	}

	plain, err := s.AnalysisRun(startedRun(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Prediction {
		t.Error("a run nobody asked to predict reads as a prediction")
	}
}

// A predicted result is what a query nobody adopted would report, and a query nobody adopted has
// found nothing yet: the counts and the list are held against the run, and no finding, evaluation
// or sighting exists afterwards that every other surface would count.
func TestAPredictedMemberHoldsItsCountsAndItsListAndStoresNoFinding(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateAnalysisRun(predictionRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}

	if err := s.RecordPredictedMemberResult(id, predictedAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 2, Counted: true,
		ByQuery: map[string]int{"actions/unpinned-tag": 1, "actions/other": 1},
	}, twoPredicted()); err != nil {
		t.Fatalf("RecordPredictedMemberResult: %v", err)
	}

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatal(err)
	}
	m := memberNamed(t, run, "octo/app")
	if !m.Counted || m.Results != 2 || m.ByQuery["actions/unpinned-tag"] != 1 {
		t.Errorf("the member reads counted=%v %d %v, want what the prediction counted",
			m.Counted, m.Results, m.ByQuery)
	}

	listed, err := s.RunFindings(id, "octo/app", "", 100)
	if err != nil {
		t.Fatalf("RunFindings: %v", err)
	}
	if !listed.Predicted || !listed.Evaluated || len(listed.Findings) != 2 {
		t.Fatalf("the member lists %+v, want the 2 predicted results, marked as predicted", listed)
	}
	first := listed.Findings[0]
	if first.RuleID != "actions/unpinned-tag" || first.FilePath != ".github/workflows/ci.yml" ||
		first.StartLine != 12 || first.StartColumn != 15 || first.Message != "Unpinned 3rd party Action" {
		t.Errorf("the first predicted result reads %+v", first)
	}
	if first.FindingRowID != 0 {
		t.Errorf("a predicted result names finding %d, and no finding exists", first.FindingRowID)
	}
	one, err := s.RunFindings(id, "octo/app", "actions/other", 100)
	if err != nil || len(one.Findings) != 1 || one.Findings[0].RuleID != "actions/other" {
		t.Errorf("narrowed to one query the member lists %+v (%v)", one.Findings, err)
	}

	for _, table := range []string{"abom_finding", "abom_query_run", "abom_finding_sighting", "repository"} {
		if n := countRows(t, s, table); n != 0 {
			t.Errorf("a prediction left %d rows in %s", n, table)
		}
	}
}

// Predicting again replaces what the member would report rather than adding to it, or a second
// pass would double every count and list each result twice.
func TestPredictingAgainReplacesWhatTheMemberWouldReport(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateAnalysisRun(predictionRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	for _, predicted := range [][]PredictedResult{twoPredicted(), twoPredicted()[:1]} {
		if err := s.RecordPredictedMemberResult(id, predictedAt(), MemberResult{
			Status: AnalysisSucceeded, Results: len(predicted), Counted: true,
			ByQuery: map[string]int{"actions/unpinned-tag": 1},
		}, predicted); err != nil {
			t.Fatalf("RecordPredictedMemberResult: %v", err)
		}
	}
	listed, err := s.RunFindings(id, "octo/app", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Findings) != 1 {
		t.Errorf("after predicting again the member lists %d results, want the 1 the last pass found",
			len(listed.Findings))
	}
}

// A prediction stores no evaluation, so nothing it made can answer a later run: a run skipped on a
// prediction's answer would record no finding for work that found something. And a run that is not
// a prediction holds no predicted results, which would read as findings nobody stored.
func TestAPredictionAndARunCannotHoldEachOthersResults(t *testing.T) {
	s := newTestStore(t)
	prediction, err := s.CreateAnalysisRun(predictionRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	ledger, err := s.RecordQueryRun(QueryRun{
		QueryID: "actions/unpinned-tag", QueryVersion: "u1", CLIVersion: "2.27.1",
		Repository: "octo/app", DBCommit: "sha-one", SourceFilter: "f0",
		AnalysisMode: AnalysisModeRegular,
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if err := s.RecordAnalysisMemberResult(prediction, predictedAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 2, Counted: true, QueryRunID: ledger,
	}); err == nil {
		t.Error("a prediction's member was linked to an evaluation")
	}

	run := startedRun(t, s)
	if err := s.RecordPredictedMemberResult(run, predictedAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 2, Counted: true,
	}, twoPredicted()); err == nil {
		t.Error("a run that is not a prediction held predicted results")
	}
	if n := countRows(t, s, "abom_predicted_result"); n != 0 {
		t.Errorf("a refused write left %d predicted results", n)
	}
}

// Evidence names a finding a run stored, and a prediction stored none. Refused for being a
// prediction, rather than as a member the run never evaluated, which it did.
func TestAPredictionsResultsAreRefusedAsEvidenceSayingWhy(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateAnalysisRun(predictionRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	if err := s.RecordPredictedMemberResult(id, predictedAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 0, Counted: true,
		ByQuery: map[string]int{"actions/unpinned-tag": 0},
	}, nil); err != nil {
		t.Fatalf("RecordPredictedMemberResult: %v", err)
	}
	err = s.CheckPicked(id, "actions/unpinned-tag", []RequestEvidence{
		{RunID: id, Repository: "octo/app", Kind: RequestMissed},
	})
	if err == nil || !strings.Contains(err.Error(), "prediction") {
		t.Errorf("a pick from a prediction answered %v, want a refusal saying it is a prediction", err)
	}
}
