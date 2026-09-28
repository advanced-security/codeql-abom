package store

import "testing"

// The Explorer draws one node per dependency while the queue analyzes one row per reference, so a
// node has to carry how much of itself has been looked at. Otherwise a partly analyzed node is
// indistinguishable from one fully covered.
func TestANodeReportsHowManyOfItsReferencesAreAnalyzed(t *testing.T) {
	s := newTestStore(t)
	externalNode(t, s, "actions/checkout", "action")

	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('actions/checkout', 'v4', 'actions', 'checkout', 1, 'done', '2026-01-01T00:00:00Z'),
		       ('actions/checkout', 'v3', 'actions', 'checkout', 1, 'pending', '2026-01-01T00:00:00Z'),
		       ('actions/checkout', 'main', 'actions', 'checkout', 1, 'pending', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}

	graph, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	var got *GraphNode
	for i := range graph.Nodes {
		if graph.Nodes[i].Key == "actions/checkout" {
			got = &graph.Nodes[i]
			break
		}
	}
	if got == nil {
		t.Fatal("actions/checkout is not in the graph")
	}
	if got.RefsInUse != 3 {
		t.Errorf("RefsInUse = %d, want the three references queued for it", got.RefsInUse)
	}
	if got.RefsAnalyzed != 1 {
		t.Errorf("RefsAnalyzed = %d, want only the one with results", got.RefsAnalyzed)
	}
	// Kept as it was: something *has* been analyzed here. It is the pair above that lets a reader
	// tell that apart from fully covered, which one boolean never could.
	if !got.Analyzed {
		t.Error("Analyzed = false, want true: one reference does have results")
	}
}

// A node nothing has ever queued has no references to report, and zero-of-zero must not read as
// fully covered — the same rule that stops an unmeasured node being drawn at the smallest radius.
func TestANodeNothingHasQueuedReportsNoReferencesRatherThanFullCoverage(t *testing.T) {
	s := newTestStore(t)
	externalNode(t, s, "actions/never-seen", "action")

	graph, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	for _, n := range graph.Nodes {
		if n.Key != "actions/never-seen" {
			continue
		}
		if n.RefsInUse != 0 || n.RefsAnalyzed != 0 {
			t.Errorf("RefsInUse/RefsAnalyzed = %d/%d, want 0/0 for something never queued",
				n.RefsInUse, n.RefsAnalyzed)
		}
		if n.Analyzed {
			t.Error("Analyzed = true for a node nothing has looked at")
		}
		return
	}
	t.Fatal("actions/never-seen is not in the graph")
}
