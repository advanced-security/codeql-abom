package store

import (
	"maps"
	"testing"
)

// A version's metadata is on its catalog row before anything runs it: the catalog is where a
// reader decides what to run, and a severity learned only from a run's SARIF arrives too late.
func TestACatalogVersionCarriesEveryPairItsQueryDeclaresBeforeAnyRun(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordSuiteContents("abom-findings.qls", []SuiteQuery{
		{Pack: "codeql/actions-queries", QueryID: "actions/untrusted-checkout/critical",
			QueryVersion: "c1", Kind: "path-problem"},
		{Pack: "codeql/actions-queries", QueryID: "actions/unnecessary-use-of-advanced-config",
			QueryVersion: "a1", Kind: "problem"},
		{Pack: "codeql/actions-queries", QueryID: "actions/never-resolved",
			QueryVersion: "n1", Kind: "problem"},
	}); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}
	critical := map[string]string{
		"id": "actions/untrusted-checkout/critical", "kind": "path-problem",
		"problem.severity": "error", "security-severity": "9.3", "precision": "very-high",
	}
	if err := s.RecordQueryMetadata([]QueryMetadata{
		{QueryID: "actions/untrusted-checkout/critical", QueryVersion: "c1", Pairs: critical},
		{QueryID: "actions/unnecessary-use-of-advanced-config", QueryVersion: "a1",
			Pairs: map[string]string{"id": "actions/unnecessary-use-of-advanced-config",
				"kind": "problem", "problem.severity": "recommendation"}},
	}); err != nil {
		t.Fatalf("RecordQueryMetadata: %v", err)
	}

	by := catalogBy(t, s)
	got := by["actions/untrusted-checkout/critical@c1"]
	if !maps.Equal(got.Metadata, critical) {
		t.Errorf("Metadata = %v, want every pair the query declares", got.Metadata)
	}
	if got.Band != "critical" || got.Runs != 0 {
		t.Errorf("band %q after %d runs, want critical before any", got.Band, got.Runs)
	}
	if unscored := by["actions/unnecessary-use-of-advanced-config@a1"]; unscored.Band != "unknown" {
		t.Errorf("a query declaring no score is banded %q, want unknown", unscored.Band)
	}
	never := by["actions/never-resolved@n1"]
	if never.Metadata != nil || never.Band != "" {
		t.Errorf("a version nobody resolved carries %v and band %q, want neither: absent is not "+
			"a query declaring nothing", never.Metadata, never.Band)
	}
}

// A version is re-resolved every round, and what it declares is replaced rather than accumulated:
// a key the query stopped declaring must not linger as something it still says.
func TestRecordingAVersionsMetadataAgainReplacesIt(t *testing.T) {
	s := newTestStore(t)
	record := func(pairs map[string]string) {
		t.Helper()
		if err := s.RecordQueryMetadata([]QueryMetadata{
			{QueryID: "abom/q", QueryVersion: "v1", Pairs: pairs}}); err != nil {
			t.Fatalf("RecordQueryMetadata: %v", err)
		}
	}
	record(map[string]string{"id": "abom/q", "security-severity": "5.0"})
	record(map[string]string{"id": "abom/q"})

	held, err := s.queryMetadata()
	if err != nil {
		t.Fatalf("queryMetadata: %v", err)
	}
	if got := held["abom/q\x00v1"]; !maps.Equal(got, map[string]string{"id": "abom/q"}) {
		t.Errorf("held %v after the key was dropped, want only what the query declares now", got)
	}
}
