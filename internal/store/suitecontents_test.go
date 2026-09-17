package store

import (
	"path/filepath"
	"testing"
)

func provenanceStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

const findingsSuite = "advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls"

// A suite is a program over packs, so what it contains is only knowable by resolving it. What
// the crawl resolved is therefore the only honest answer to "what does this suite run".
func TestWhatASuiteResolvedToIsReadBackAsItWasRecorded(t *testing.T) {
	s := provenanceStore(t)
	queries := []SuiteQuery{
		{Pack: "codeql/actions-queries", PackVersion: "0.6.34", QueryID: "actions/unpinned-tag", Kind: "problem"},
		{Pack: "codeql/actions-queries", PackVersion: "0.6.34", QueryID: "actions/code-injection/medium", Kind: "problem"},
	}
	if err := s.RecordSuiteContents(findingsSuite, queries); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}

	got, err := s.SuiteContents()
	if err != nil {
		t.Fatalf("SuiteContents: %v", err)
	}
	if len(got[findingsSuite]) != 2 {
		t.Fatalf("got %v, want both queries", got[findingsSuite])
	}
	if got[findingsSuite][0].PackVersion != "0.6.34" {
		t.Errorf("pack version = %q, want the version the crawl actually resolved",
			got[findingsSuite][0].PackVersion)
	}
}

// A suite is re-resolved every round. Accumulating would make a query removed upstream look like
// it is still running.
func TestResolvingASuiteAgainReplacesWhatItHeldBefore(t *testing.T) {
	s := provenanceStore(t)
	if err := s.RecordSuiteContents(findingsSuite, []SuiteQuery{
		{Pack: "codeql/actions-queries", PackVersion: "0.6.34", QueryID: "actions/retired", Kind: "problem"},
	}); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}
	if err := s.RecordSuiteContents(findingsSuite, []SuiteQuery{
		{Pack: "codeql/actions-queries", PackVersion: "0.6.35", QueryID: "actions/unpinned-tag", Kind: "problem"},
	}); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}

	got, err := s.SuiteContents()
	if err != nil {
		t.Fatalf("SuiteContents: %v", err)
	}
	if len(got[findingsSuite]) != 1 || got[findingsSuite][0].QueryID != "actions/unpinned-tag" {
		t.Errorf("got %v, want only what the latest resolution held", got[findingsSuite])
	}
}

// Never resolved is not the same as resolved to nothing, and an interface that showed 0 for a
// suite nobody has bound would be asserting a measurement it never took.
func TestASuiteNobodyResolvedIsAbsentRatherThanEmpty(t *testing.T) {
	s := provenanceStore(t)
	if err := s.RecordSuiteContents(findingsSuite, []SuiteQuery{
		{Pack: "codeql/actions-queries", PackVersion: "0.6.34", QueryID: "actions/unpinned-tag"},
	}); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}

	got, err := s.SuiteContents()
	if err != nil {
		t.Fatalf("SuiteContents: %v", err)
	}
	if _, ok := got["advanced-security/abom-actions-selfcheck-src:codeql-suites/abom-selfcheck.qls"]; ok {
		t.Error("a suite nobody resolved has an entry, which reads as a measured zero")
	}
}

// A suite may legitimately draw from several packs at once, and each carries its own version.
func TestASuiteDrawingFromSeveralPacksKeepsEachVersion(t *testing.T) {
	s := provenanceStore(t)
	mixed := "advanced-security/abom-actions-graph-extraction-src:codeql-suites/abom-dependency-graph.qls"
	if err := s.RecordSuiteContents(mixed, []SuiteQuery{
		{Pack: "advanced-security/abom-actions-graph-extraction-src", PackVersion: "2.26.4", QueryID: "abom/edges"},
		{Pack: "codeql/actions-queries", PackVersion: "0.6.34", QueryID: "actions/unpinned-tag", Kind: "problem"},
	}); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}

	got, err := s.SuiteContents()
	if err != nil {
		t.Fatalf("SuiteContents: %v", err)
	}
	versions := map[string]string{}
	for _, q := range got[mixed] {
		versions[q.Pack] = q.PackVersion
	}
	if versions["advanced-security/abom-actions-graph-extraction-src"] != "2.26.4" ||
		versions["codeql/actions-queries"] != "0.6.34" {
		t.Errorf("versions = %v, want each pack to keep its own", versions)
	}
}
