package store

import "testing"

// The Discovery queue is read as "what happens next". Ordering it differently from the order
// Claim actually takes would show an expedited dependency buried in the middle of a list while
// the crawl runs it first.
func TestTheQueueViewShowsExpeditedWorkWhereItWillRun(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "ordinary", 1, "pending")
	tierRow(t, s, "asked-for", 1, "pending")
	if _, err := s.DB().Exec(
		`UPDATE abom_frontier SET score = 99 WHERE node_key = 'ordinary'`); err != nil {
		t.Fatalf("scoring: %v", err)
	}
	if _, err := s.DB().Exec(
		`UPDATE abom_frontier SET expedited_at = '2026-01-02T00:00:00Z'
		 WHERE node_key = 'asked-for'`); err != nil {
		t.Fatalf("expediting: %v", err)
	}

	items, err := s.FrontierSnapshot("", 10)
	if err != nil {
		t.Fatalf("FrontierSnapshot: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].NodeKey != "asked-for" {
		t.Errorf("the queue lists %q first, but Claim would take the expedited item",
			items[0].NodeKey)
	}
}
