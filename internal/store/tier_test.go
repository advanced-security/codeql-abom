package store

import "testing"

func tierRow(t *testing.T, s *Store, key string, depth int, status string) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, owner, repo, depth, status, score, enqueued_at,
			analysis_count, resolved_sha)
		VALUES (?, 'octo', ?, ?, ?, 1, '2026-01-01T00:00:00Z', 1, 'sha')`,
		key, key, depth, status); err != nil {
		t.Fatalf("inserting %s: %v", key, err)
	}
}

// Re-processing has to be expressible at the granularity someone actually reasons about. "Run
// the new suite over everything transitive" is a sentence a reader can mean; "re-run rows 400
// to 900" is not.
func TestATierCanBeRequeuedOnItsOwn(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "direct-one", 1, "done")
	tierRow(t, s, "transitive-one", 2, "done")
	tierRow(t, s, "transitive-two", 4, "done")

	n, err := s.RequeueTier(TierTransitive)
	if err != nil {
		t.Fatalf("RequeueTier: %v", err)
	}
	if n != 2 {
		t.Errorf("requeued %d, want both transitive dependencies", n)
	}

	if got := statusOfKey(t, s, "direct-one"); got != "done" {
		t.Errorf("direct-one = %q, want an untouched tier left alone", got)
	}
	for _, key := range []string{"transitive-one", "transitive-two"} {
		if got := statusOfKey(t, s, key); got != "pending" {
			t.Errorf("%s = %q, want pending", key, got)
		}
	}
}

// Depth 1 is what a repository under review names itself, and is the tier whose fix is entirely
// in the reader's hands, so it has to be selectable apart from everything reached through it.
func TestDirectAndTransitiveAreSeparateTiers(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "direct-one", 1, "done")
	tierRow(t, s, "transitive-one", 2, "done")

	if _, err := s.RequeueTier(TierDirect); err != nil {
		t.Fatalf("RequeueTier: %v", err)
	}
	if got := statusOfKey(t, s, "direct-one"); got != "pending" {
		t.Errorf("direct-one = %q, want pending", got)
	}
	if got := statusOfKey(t, s, "transitive-one"); got != "done" {
		t.Errorf("transitive-one = %q, want left alone", got)
	}
}

// Requeueing must not charge an attempt or spend a retry: nothing failed, the work is simply
// being asked for again. Charging it would let a few re-runs exhaust a dependency's budget.
func TestRequeueingDoesNotSpendARetry(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "direct-one", 1, "done")
	if _, err := s.DB().Exec(
		`UPDATE abom_frontier SET attempts = 2 WHERE node_key = 'direct-one'`); err != nil {
		t.Fatalf("setting attempts: %v", err)
	}

	if _, err := s.RequeueTier(TierDirect); err != nil {
		t.Fatalf("RequeueTier: %v", err)
	}

	var attempts int
	var next any
	if err := s.DB().QueryRow(
		`SELECT attempts, next_attempt_at FROM abom_frontier WHERE node_key='direct-one'`).
		Scan(&attempts, &next); err != nil {
		t.Fatalf("reading item: %v", err)
	}
	if attempts != 0 || next != nil {
		t.Errorf("attempts=%d next=%v, want a clean slate rather than a charged retry",
			attempts, next)
	}
}

// What was learned last time stays until it is replaced. Clearing it on requeue would blank the
// Explorer for as long as the re-run takes, which is the opposite of what re-processing is for.
func TestRequeueingKeepsWhatIsAlreadyKnown(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "direct-one", 1, "done")

	if _, err := s.RequeueTier(TierDirect); err != nil {
		t.Fatalf("RequeueTier: %v", err)
	}

	var sha string
	var analyses int
	if err := s.DB().QueryRow(
		`SELECT COALESCE(resolved_sha,''), analysis_count FROM abom_frontier
		 WHERE node_key='direct-one'`).Scan(&sha, &analyses); err != nil {
		t.Fatalf("reading item: %v", err)
	}
	if sha == "" || analyses == 0 {
		t.Errorf("sha=%q analyses=%d, want the previous analysis still on record", sha, analyses)
	}
}

// A dependency nothing can be fetched for stays that way: re-running the same impossible
// checkout is the one outcome already known in advance.
func TestRequeueingLeavesUnfetchableDependenciesAlone(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "gone", 1, "unavailable")

	if _, err := s.RequeueTier(TierDirect); err != nil {
		t.Fatalf("RequeueTier: %v", err)
	}
	if got := statusOfKey(t, s, "gone"); got != "unavailable" {
		t.Errorf("gone = %q, want it left unavailable", got)
	}
}

func TestAnUnknownTierIsRefusedRatherThanMatchingNothing(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.RequeueTier("everything"); err == nil {
		t.Error("an unknown tier was accepted, which would silently requeue nothing")
	}
}

func statusOfKey(t *testing.T, s *Store, key string) string {
	t.Helper()
	var status string
	if err := s.DB().QueryRow(
		`SELECT status FROM abom_frontier WHERE node_key = ?`, key).Scan(&status); err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	return status
}
