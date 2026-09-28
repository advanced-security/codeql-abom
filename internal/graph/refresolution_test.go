package graph

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The crawl's resolved tag-to-commit pair must be recorded for later graph walks.
func TestRebuildAggregatesRecordsWhatTheCrawlResolved(t *testing.T) {
	s := newStore(t)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, score,
			enqueued_at, analysis_count, resolved_sha)
		VALUES ('actions/checkout', 'v4.2.2', 'actions', 'checkout', 1, 'done', 1,
			'2026-01-01T00:00:00Z', 1, 'a1b2c3d4e5f60718293a4b5c6d7e8f9012345678')`); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}

	if err := (&Builder{Store: s}).RebuildAggregates(); err != nil {
		t.Fatalf("RebuildAggregates: %v", err)
	}

	got, ok, err := s.SemverForSHA("actions", "checkout", "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678")
	if err != nil {
		t.Fatalf("SemverForSHA: %v", err)
	}
	if !ok || got != "v4.2.2" {
		t.Errorf("SemverForSHA = %q, %v; the rebuild must record what the crawl resolved", got, ok)
	}
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir()+"/abom.db", store.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}
