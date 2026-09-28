package store

import (
	"strings"
	"testing"
)

const suggestionSuite = "advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls"

// suggestionStore holds an upstream query, an ABOM query a tier runs, and a custom query at two
// versions, one of them at deploy.
func suggestionStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.RecordSuiteContents(suggestionSuite, []SuiteQuery{
		{Pack: "codeql/actions-queries", PackVersion: "0.6.36", QueryID: "actions/unpinned-tag", QueryVersion: "up1"},
		{Pack: "advanced-security/abom-actions-findings-src", QueryID: "abom/marker", QueryVersion: "ab1"},
	}); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}
	if err := s.RecordSuiteContents("abom/custom-queries:codeql-suites/abom-custom.qls", []SuiteQuery{
		{Pack: "abom/custom-queries", QueryID: "acme/pin", QueryVersion: "c2"},
		{Pack: "abom/custom-queries", QueryID: "acme/draft", QueryVersion: "d1"},
	}); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}
	if err := s.SetTierSuites(TierDirect, []string{suggestionSuite}); err != nil {
		t.Fatalf("SetTierSuites: %v", err)
	}
	for _, stage := range []struct{ id, version, stage string }{
		{"acme/pin", "c1", StageDeploy},
		{"acme/draft", "d1", StageExtendedTest},
	} {
		if err := s.AdvanceQueryStage(stage.id, stage.version, stage.stage, "operator", "2.27.1"); err != nil {
			t.Fatalf("AdvanceQueryStage %s: %v", stage.id, err)
		}
	}
	return s
}

func suggested(t *testing.T, s *Store, queries ...AnalysisQuery) FindingsSuggestion {
	t.Helper()
	got, err := s.SuggestFindings(queries)
	if err != nil {
		t.Fatalf("SuggestFindings: %v", err)
	}
	if len(got.Queries) != len(queries) {
		t.Fatalf("the suggestion speaks for %d queries, want all %d", len(got.Queries), len(queries))
	}
	return got
}

// A version nobody changed from what a pack published or a suite promoted reports what its
// promotion already stands behind, so its results are findings unless somebody says otherwise.
func TestAPublishedOrPromotedVersionSuggestsFindings(t *testing.T) {
	s := suggestionStore(t)
	if err := s.AdvanceQueryStage("acme/pin", "c2", StageDeploy, "operator", "2.27.1"); err != nil {
		t.Fatalf("AdvanceQueryStage: %v", err)
	}
	for _, q := range []AnalysisQuery{
		{QueryID: "actions/unpinned-tag", QueryVersion: "up1"},
		{QueryID: "abom/marker", QueryVersion: "ab1"},
		{QueryID: "acme/pin", QueryVersion: "c2"},
	} {
		got := suggested(t, s, q)
		if !got.Findings || !got.Queries[0].Promoted || got.Queries[0].Why == "" {
			t.Errorf("%s@%s suggests %+v, want findings on, saying why", q.QueryID, q.QueryVersion, got)
		}
	}
}

// A version still being refined suggests holding, and the reason names the query and what it is
// short of — including a version edited since the one somebody deployed.
func TestAVersionStillBeingRefinedSuggestsHoldingAndSaysWhy(t *testing.T) {
	s := suggestionStore(t)
	for _, c := range []struct {
		q    AnalysisQuery
		want string
	}{
		{AnalysisQuery{QueryID: "acme/draft", QueryVersion: "d1"}, StageExtendedTest},
		{AnalysisQuery{QueryID: "acme/pin", QueryVersion: "c2"}, "deploy"},
		{AnalysisQuery{QueryID: "acme/new", QueryVersion: "n1"}, "stage"},
	} {
		got := suggested(t, s, c.q)
		if got.Findings || got.Queries[0].Promoted {
			t.Errorf("%s@%s suggests findings on (%+v), want off", c.q.QueryID, c.q.QueryVersion, got)
		}
		if !strings.Contains(got.Queries[0].Why, c.want) || !strings.Contains(got.Reason, c.q.QueryID) {
			t.Errorf("%s@%s says %q / %q, want the reason to name it and %q",
				c.q.QueryID, c.q.QueryVersion, got.Queries[0].Why, got.Reason, c.want)
		}
	}
}

// One setting covers every query in the run, so one query still being refined holds all of it.
func TestOneQueryStillBeingRefinedHoldsTheWholeRun(t *testing.T) {
	s := suggestionStore(t)
	got := suggested(t, s,
		AnalysisQuery{QueryID: "actions/unpinned-tag", QueryVersion: "up1"},
		AnalysisQuery{QueryID: "acme/draft", QueryVersion: "d1"})
	if got.Findings {
		t.Errorf("a run holding a query at extended-test suggests findings on: %+v", got)
	}
	if !got.Queries[0].Promoted || got.Queries[1].Promoted {
		t.Errorf("per query the suggestion reads %+v, want the upstream one promoted and the draft not",
			got.Queries)
	}
	if !strings.Contains(got.Reason, "acme/draft") {
		t.Errorf("the reason %q does not name the query holding the run", got.Reason)
	}
}

// A run names at least one query, so an empty list is a caller's mistake rather than a run
// whose every query is promoted.
func TestNoQueriesSuggestsNothing(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.SuggestFindings(nil); err == nil {
		t.Error("an empty list of queries was given a suggestion")
	}
}
