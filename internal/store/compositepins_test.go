package store

import "testing"

// The projection can only report a transitive dependency if the parent was analyzed at the ref
// the consumer pinned. On the real corpus 35 of the 47 composite actions a seed workflow pins had
// never been analyzed at that ref, so the closure reported 3 transitive packages across 402
// repositories — a number floored by coverage rather than by the shape of the ecosystem.
func TestCompositeDependenciesPinnedButNeverAnalyzedAreReported(t *testing.T) {
	s := newTestStore(t)
	projectionFixture(t, s)

	// v1 was analyzed by the fixture; nothing has analyzed v3.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_edge (edge_key, src_node_row_id, dst_node_row_id, edge_kind,
		                       ref_value, file_path, repository_row_id, start_line)
		SELECT 'e5', src_node_row_id, dst_node_row_id, 'step-uses-action', 'v3',
		       '.github/workflows/ci.yml', repository_row_id, 2
		  FROM abom_edge WHERE edge_key = 'e1'`); err != nil {
		t.Fatalf("adding a second pinned ref: %v", err)
	}

	got, err := s.UnanalyzedCompositePins()
	if err != nil {
		t.Fatalf("UnanalyzedCompositePins: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("reported %d unanalyzed pins, want 1: %+v", len(got), got)
	}
	if got[0].NodeKey != "vendor/action" || got[0].Ref != "v3" {
		t.Errorf("reported %+v, want vendor/action at v3", got[0])
	}
}

// A ref that has been analyzed is not a gap. Reporting it would make the queue re-analyze work it
// has already done, and would make the coverage number never reach zero.
func TestARefAlreadyAnalyzedIsNotReportedAsAGap(t *testing.T) {
	s := newTestStore(t)
	projectionFixture(t, s)

	got, err := s.UnanalyzedCompositePins()
	if err != nil {
		t.Fatalf("UnanalyzedCompositePins: %v", err)
	}
	for _, g := range got {
		if g.Ref == "v1" {
			t.Errorf("v1 is analyzed but was reported as a gap: %+v", g)
		}
	}
}

// A dependency that is not a composite action cannot have children, so queueing it at every
// pinned ref would spend the crawl on work that cannot produce a transitive edge.
func TestANonCompositeDependencyIsNotReported(t *testing.T) {
	s := newTestStore(t)
	projectionFixture(t, s)

	got, err := s.UnanalyzedCompositePins()
	if err != nil {
		t.Fatalf("UnanalyzedCompositePins: %v", err)
	}
	for _, g := range got {
		if g.NodeKey == "helper/tool" || g.NodeKey == "vendor/ci-only" {
			t.Errorf("a non-composite dependency was reported: %+v", g)
		}
	}
}
