package expand

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// declared is metadata as the CLI prints it, read the way ResolveMetadata reads it, so every pair
// arrives rather than the four the struct names.
func declared(t *testing.T, id, kind, score string) runner.Metadata {
	t.Helper()
	pairs := map[string]string{"id": id, "kind": kind, "problem.severity": "error",
		"precision": "very-high", "tags": "actions security"}
	if score != "" {
		pairs["security-severity"] = score
	}
	body, err := json.Marshal(pairs)
	if err != nil {
		t.Fatal(err)
	}
	var md runner.Metadata
	if err := json.Unmarshal(body, &md); err != nil {
		t.Fatalf("reading metadata: %v", err)
	}
	return md
}

func catalogRow(t *testing.T, s *store.Store, id string) store.CatalogQuery {
	t.Helper()
	rows, err := s.QueryCatalog()
	if err != nil {
		t.Fatalf("QueryCatalog: %v", err)
	}
	for _, r := range rows {
		if r.QueryID == id {
			return r
		}
	}
	t.Fatalf("the catalog does not list %s", id)
	return store.CatalogQuery{}
}

// A round that resolves a suite records everything each query declares, against the version it
// resolved: the catalog then says what a query reports at before anything has run it.
func TestARoundRecordsEveryPairEachResolvedQueryDeclares(t *testing.T) {
	h := newHarness(t)
	query := queryInPack(t, t.TempDir(), "codeql/actions-queries", "0.6.35", "Critical.ql",
		"select 1")
	h.expander.Resolver = &fakeResolver{
		queries: []string{query},
		meta: map[string]runner.Metadata{
			query: declared(t, "actions/untrusted-checkout/critical", "path-problem", "9.3")},
	}
	for _, tier := range []string{store.TierOwned, store.TierDirect, store.TierTransitive} {
		if err := h.store.SetTierSuites(tier, []string{"findings.qls"}); err != nil {
			t.Fatalf("SetTierSuites: %v", err)
		}
	}
	if _, err := h.expander.plansByTier(context.Background(),
		Options{WorkDir: t.TempDir()}); err != nil {
		t.Fatalf("plansByTier: %v", err)
	}

	row := catalogRow(t, h.store, "actions/untrusted-checkout/critical")
	if len(row.Metadata) != 6 || row.Metadata["precision"] != "very-high" ||
		row.Metadata["security-severity"] != "9.3" {
		t.Errorf("recorded %v, want every pair the query declares", row.Metadata)
	}
	if row.Band != "critical" {
		t.Errorf("band %q, want critical from the declared 9.3", row.Band)
	}
}

// A suite named for one invocation is resolved like a bound one, so what it declares is recorded
// too, and the catalog row its run adds carries it.
func TestASuiteNamedOnceRecordsWhatItsQueriesDeclare(t *testing.T) {
	h := newHarness(t)
	query := queryInPack(t, t.TempDir(), "codeql/actions-queries", "0.6.35", "Unscored.ql",
		"select 1")
	h.expander.Resolver = &fakeResolver{
		queries: []string{query},
		meta: map[string]runner.Metadata{
			query: declared(t, "actions/unnecessary-use-of-advanced-config", "problem", "")},
	}
	if _, err := h.expander.plansByTier(context.Background(),
		Options{WorkDir: t.TempDir(), Suite: "once.qls"}); err != nil {
		t.Fatalf("plansByTier: %v", err)
	}
	version, err := runner.QueryVersion(query)
	if err != nil {
		t.Fatalf("QueryVersion: %v", err)
	}
	if _, err := h.store.RecordQueryRun(store.QueryRun{
		QueryID: "actions/unnecessary-use-of-advanced-config", QueryVersion: version,
		Repository: "octo/app", DBCommit: "sha", AnalysisMode: store.AnalysisModeRegular,
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	row := catalogRow(t, h.store, "actions/unnecessary-use-of-advanced-config")
	if row.Metadata["kind"] != "problem" || row.Band != "unknown" {
		t.Errorf("metadata %v, band %q: want what it declares, and unknown for no score",
			row.Metadata, row.Band)
	}
}

// An on-demand run resolves every query it runs, and records what each declares against the
// version it ran.
func TestAnOnDemandRunRecordsWhatItsQueriesDeclare(t *testing.T) {
	s, e, id, _ := executorFixture(t)
	resolver := e.Resolver.(*fakeResolver)
	query := resolver.queries[0]
	resolver.meta[query] = declared(t, "actions/unpinned-tag", "problem", "5.0")

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	row := catalogRow(t, s, "actions/unpinned-tag")
	if row.Metadata["security-severity"] != "5.0" || row.Band != "medium" {
		t.Errorf("metadata %v, band %q: want the version's declaration, banded medium",
			row.Metadata, row.Band)
	}
	if filepath.Base(query) != "Unpinned.ql" {
		t.Fatalf("the fixture's query moved: %s", query)
	}
}
