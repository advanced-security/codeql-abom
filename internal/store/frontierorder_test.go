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

// A moved reference waits behind everything never analyzed, so the queue view lists it there.
func TestTheQueueViewShowsAMovedReferenceAfterWorkNeverAnalyzed(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "moved", 1, "pending")
	tierRow(t, s, "deep", 3, "pending")
	if _, err := s.DB().Exec(
		`UPDATE abom_frontier SET requeued_for = 'moved' WHERE node_key = 'moved'`); err != nil {
		t.Fatalf("marking: %v", err)
	}

	items, err := s.FrontierSnapshot("", 10)
	if err != nil {
		t.Fatalf("FrontierSnapshot: %v", err)
	}
	if len(items) != 2 || items[0].NodeKey != "deep" {
		t.Errorf("the queue lists %+v, but Claim takes the unanalyzed dependency first", items)
	}
}

// A push whose Actions dependencies moved is claimed first among the pushes waiting with it, so the
// queue view lists it there.
func TestTheQueueViewShowsAPushWhoseDependenciesMovedFirst(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "stale", 0, "pending")
	tierRow(t, s, "recent", 0, "pending")
	if _, err := s.DB().Exec(`UPDATE abom_frontier SET requeued_for = 'moved',
		expedited_at = '2026-01-02T00:00:00Z',
		analyzed_at = CASE node_key WHEN 'stale' THEN '2026-01-01T00:00:00Z' ELSE '2026-06-01T00:00:00Z' END,
		dependencies_moved = CASE node_key WHEN 'recent' THEN 1 END`); err != nil {
		t.Fatalf("marking: %v", err)
	}

	items, err := s.FrontierSnapshot("", 10)
	if err != nil {
		t.Fatalf("FrontierSnapshot: %v", err)
	}
	if len(items) != 2 || items[0].NodeKey != "recent" {
		t.Errorf("the queue lists %+v, but Claim takes the push whose dependencies moved first", items)
	}
}
