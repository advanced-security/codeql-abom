package store

import "testing"

// The queue held one row per dependency, so every reference collapsed onto one analysis of the
// default branch: a repository pinning v3 was reported from a reading of main. Coverage is the
// count that makes that gap visible, and it has to distinguish analyzed from merely queued from
// never queued at all.
func TestCoverageCountsVersionsInUseAgainstVersionsAnalyzed(t *testing.T) {
	s := newTestStore(t)
	externalNode(t, s, "actions/checkout", "action")

	var dst int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM abom_node WHERE node_key = 'actions/checkout'`).Scan(&dst); err != nil {
		t.Fatalf("finding the node: %v", err)
	}
	for i, ref := range []string{"v4", "v3", "main"} {
		if _, err := s.DB().Exec(`
			INSERT INTO abom_edge (edge_key, src_node_row_id, dst_node_row_id, edge_kind,
			                       ref_value, start_line)
			VALUES (?, ?, ?, 'step-uses-action', ?, ?)`,
			"e"+ref, dst, dst, ref, i); err != nil {
			t.Fatalf("recording a use at %s: %v", ref, err)
		}
	}

	// v4 analyzed, v3 still queued, main never reached.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('actions/checkout', 'v4', 'actions', 'checkout', 1, 'done', '2026-01-01T00:00:00Z'),
		       ('actions/checkout', 'v3', 'actions', 'checkout', 1, 'pending', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}

	got, err := s.RefsCovered()
	if err != nil {
		t.Fatalf("RefsCovered: %v", err)
	}
	want := RefCoverage{Observed: 3, Analyzed: 1, Queued: 1, Unqueued: 1}
	if got != want {
		t.Errorf("coverage = %+v, want %+v", got, want)
	}
}

// With nothing referenced there is no gap to report, and an empty corpus must not read as one
// fully covered — the counts are all zero rather than Analyzed equalling Observed by accident.
func TestCoverageOfAnEmptyGraphIsZeroRatherThanComplete(t *testing.T) {
	s := newTestStore(t)

	got, err := s.RefsCovered()
	if err != nil {
		t.Fatalf("RefsCovered: %v", err)
	}
	if got != (RefCoverage{}) {
		t.Errorf("coverage = %+v, want every count zero", got)
	}
}
