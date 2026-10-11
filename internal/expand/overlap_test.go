package expand

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// overlapFixture is the crawl's findings round over actions/checkout and a run of one query over
// the same repository, at the commit the crawl fetches, sharing one store and one writer.
func overlapFixture(t *testing.T) (*harness, *Executor, int64) {
	t.Helper()
	h, _ := findingsHarness(t)
	h.enqueue(item("actions", "checkout", "", 1))
	sha, _ := h.probe.state()

	root := t.TempDir()
	query := queryInPack(t, root, "codeql/actions-queries", "0.6.35", "Unpinned.ql", "select 1")
	e := &Executor{
		Store: h.store,
		Resolver: &fakeResolver{
			queries: []string{query},
			meta:    map[string]runner.Metadata{query: {ID: "actions/unpinned-tag", Kind: "problem"}},
		},
		Analyzer: &fakeAnalyzer{},
		Ingester: h.expander.Ingester,
		Crawl:    h.expander,
		Suites:   []string{"abom-findings.qls"},
		WorkDir:  filepath.Join(root, "runs"),
	}
	id := runOver(t, h.store, store.AnalysisModeRegular, []store.ScopedDatabase{{
		DatabaseIdentity: store.DatabaseIdentity{
			Repository: "actions/checkout", Commit: sha,
			SourceFilter: h.expander.sourceFilter(), Mode: store.AnalysisModeRegular,
		},
		Status: store.DatabaseBuildable,
	}}, nil)
	return h, e, id
}

// findingRows is every finding as what identifies it, sorted, and whether any identity is stored
// twice.
func findingRows(t *testing.T, s *store.Store) ([]string, bool) {
	t.Helper()
	rows, err := s.DB().Query(`
		SELECT f.rule_id, r.repository_full_name, COALESCE(f.file_path, ''),
		       COALESCE(f.start_line, 0), f.fingerprint
		FROM abom_finding f JOIN repository r ON r.row_id = f.repository_row_id`)
	if err != nil {
		t.Fatalf("reading findings: %v", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var rule, repo, file, fingerprint string
		var line int
		if err := rows.Scan(&rule, &repo, &file, &line, &fingerprint); err != nil {
			t.Fatalf("scanning a finding: %v", err)
		}
		out = append(out, fmt.Sprintf("%s %s %s:%d %s", rule, repo, file, line, fingerprint))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading findings: %v", err)
	}
	slices.Sort(out)
	return out, len(slices.Compact(slices.Clone(out))) != len(out)
}

func runTheRun(t *testing.T, e *Executor, id int64) {
	t.Helper()
	if err := e.Run(context.Background(), id); err != nil {
		t.Errorf("Run: %v", err)
	}
}

// A run and a crawl round over the same repository and commit produce the rows either would have
// produced alone, in either order and at once: a run is a reader with a different plan, and the
// findings both report are one finding each, whoever stored it first.
func TestARunAndACrawlRoundOverlappingProduceWhatEitherWouldAlone(t *testing.T) {
	h, _, _ := overlapFixture(t)
	runBound(t, h)
	crawled, _ := findingRows(t, h.store)

	_, e, id := overlapFixture(t)
	runTheRun(t, e, id)
	ran, _ := findingRows(t, e.Store.(*store.Store))
	if len(crawled) == 0 || len(ran) == 0 {
		t.Fatalf("the crawl stored %d findings and the run %d; the comparison proves nothing",
			len(crawled), len(ran))
	}
	want := slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(crawled), ran...))))

	for name, both := range map[string]func(*harness, *Executor, int64){
		"crawl then run": func(h *harness, e *Executor, id int64) { runBound(t, h); runTheRun(t, e, id) },
		"run then crawl": func(h *harness, e *Executor, id int64) { runTheRun(t, e, id); runBound(t, h) },
		"at once": func(h *harness, e *Executor, id int64) {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); runTheRun(t, e, id) }()
			go func() {
				defer wg.Done()
				if _, err := h.expander.Run(context.Background(),
					Options{WorkDir: t.TempDir(), Limit: 10}); err != nil {
					t.Errorf("crawl: %v", err)
				}
			}()
			wg.Wait()
		},
	} {
		h, e, id := overlapFixture(t)
		both(h, e, id)
		got, twice := findingRows(t, h.store)
		if twice {
			t.Errorf("%s: a finding is stored twice: %v", name, got)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: stored %v\nwant what either stores alone, together: %v", name, got, want)
		}
		if m := membersByRepository(t, h.store, id)["actions/checkout"]; m.Status != store.AnalysisSucceeded {
			t.Errorf("%s: the run's member is %q, want %q", name, m.Status, store.AnalysisSucceeded)
		}
	}
}
