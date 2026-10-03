package serve

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// analyzedBeforeContent is vendor/comp analyzed at each commit before sightings kept content: an
// edges run at each, none marked, and a finished queue row whose reference resolved there.
func analyzedBeforeContent(t *testing.T, s *store.Store, commits ...string) {
	t.Helper()
	repo, err := s.InsertUpstreamRepository("vendor/comp")
	if err != nil {
		t.Fatal(err)
	}
	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "vendor/comp", Kind: "composite_action", Owner: "vendor", Repo: "comp",
			RepositoryID: repo, Path: "action.yml"},
		{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for i, commit := range commits {
		run, err := s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
			QueryID: "abom/edges", Repository: "vendor/comp", DBCommit: commit})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.AddEdge(store.Edge{Key: "vendor/comp:action.yml:9:9", SrcKey: "vendor/comp",
			DstKey: "actions/cache", Kind: store.EdgeKindCompositeStepUsesAction,
			RefRaw: "actions/cache@v4", RefValue: "v4", RepositoryID: repo, QueryRunID: run,
			FilePath: "action.yml", StartLine: 9, StartColumn: 9}); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`
			INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
			                           resolved_sha, analyzed_ref, analyzed_at, analysis_count)
			VALUES ('vendor/comp', ?, 'vendor', 'comp', 1, 'done', '2026-09-20T00:00:00Z', ?, ?,
			        '2026-09-21T00:00:00Z', 1)`,
			fmt.Sprintf("v%d", i+1), commit, fmt.Sprintf("v%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
}

// Re-observing what was recorded before sightings kept content is part of what the crawl is working
// on, and how much is left is a gap in what is known, not a detail of the queue. Zero is measured
// here rather than absent: the count is taken on every request.
func TestTheObjectiveSaysHowManyCommitsAreNotYetObserved(t *testing.T) {
	srv, s := newServer(t)
	unobserved := func() any {
		t.Helper()
		rec := get(t, srv, "/api/discovery/objective")
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding %s: %v", rec.Body.String(), err)
		}
		return body["unobserved"]
	}
	if got := unobserved(); got != float64(0) {
		t.Errorf("an empty store reports unobserved = %v, want 0", got)
	}

	analyzedBeforeContent(t, s, "c1", "c2")

	if got := unobserved(); got != float64(2) {
		t.Errorf("unobserved = %v, want vendor/comp's 2 commits", got)
	}
}
