package store

import "testing"

// Splitting by reference must not depend on Go's randomized map iteration. A dependency analyzed
// at several references gets a node for each of them.
func TestSplittingByReferenceIsDeterministic(t *testing.T) {
	s := newTestStore(t)
	for _, ref := range []string{"v1", "v2", "v3", "v4", "v5", "v6"} {
		enqueueRef(t, s, "acme/widget", ref, 1, "done")
	}
	nodes := []GraphNode{{Key: "acme/widget", Kind: "action", IsExternal: true, Owner: "acme"}}
	edges := []GraphEdge{{Source: "acme/widget", Target: "acme/other"}}

	first := splitKeys(t, s, nodes, edges)
	for i := range 12 {
		got := splitKeys(t, s, nodes, edges)
		if !sameSet(first, got) {
			t.Fatalf("run %d produced a different node set:\n first %v\n   got %v", i, first, got)
		}
	}
}

// Every analyzed reference is a version that ran, and its steps are already edges out of it. The
// single-valued map dropped all but one, which is what made the graph unstable.
func TestEveryAnalyzedReferenceGetsANode(t *testing.T) {
	s := newTestStore(t)
	refs := []string{"v1", "v2", "v3"}
	for _, ref := range refs {
		enqueueRef(t, s, "acme/widget", ref, 1, "done")
	}
	nodes := []GraphNode{{Key: "acme/widget", Kind: "action", IsExternal: true, Owner: "acme"}}
	edges := []GraphEdge{{Source: "acme/widget", Target: "acme/other"}}

	got := splitKeys(t, s, nodes, edges)

	for _, ref := range refs {
		want := "acme/widget@" + ref
		if !got[want] {
			t.Errorf("no node for analyzed reference %q; got %v", want, got)
		}
	}
}

// A dependency nothing analyzed still has no version to hang its edges on, so it stays whole.
// Splitting it would have to invent one.
func TestADependencyWithNoAnalyzedReferenceIsLeftWhole(t *testing.T) {
	s := newTestStore(t)
	enqueueRef(t, s, "acme/widget", "v1", 1, "pending")
	nodes := []GraphNode{{Key: "acme/widget", Kind: "action", IsExternal: true, Owner: "acme"}}
	edges := []GraphEdge{{Source: "acme/widget", Target: "acme/other"}}

	got := splitKeys(t, s, nodes, edges)

	if !got["acme/widget"] {
		t.Errorf("the unanalyzed dependency was split or dropped: %v", got)
	}
}

// An edge out of a dependency came from one checkout of it, and the bare node is gone once the
// dependency is split, so the edge has to name a version. With several analyzed references it took
// whichever the map yielded, which is how the same edge moved between versions on each read. The
// most recently analyzed reference is the one whose results are in the graph, because ingestion
// upserts.
func TestAnEdgeLeavesTheVersionWhoseResultsAreInTheGraph(t *testing.T) {
	s := newTestStore(t)
	enqueueAnalyzed(t, s, "acme/widget", "v1", "2026-09-01T00:00:00Z")
	enqueueAnalyzed(t, s, "acme/widget", "v3", "2026-09-07T00:00:00Z")
	enqueueAnalyzed(t, s, "acme/widget", "v2", "2026-09-04T00:00:00Z")
	nodes := []GraphNode{{Key: "acme/widget", Kind: "action", IsExternal: true, Owner: "acme"}}
	edges := []GraphEdge{{Source: "acme/widget", Target: "acme/other"}}

	for range 8 {
		_, out, err := s.splitNodesByRef(
			append([]GraphNode(nil), nodes...), append([]GraphEdge(nil), edges...))
		if err != nil {
			t.Fatalf("splitNodesByRef: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("got %d edges, want 1", len(out))
		}
		if out[0].Source != "acme/widget@v3" {
			t.Fatalf("edge leaves %q, want the latest analyzed version", out[0].Source)
		}
	}
}

// Every edge must still leave a node that exists, which is the reason the bare node is not kept.
func TestNoEdgeLeavesANodeThatWasSplitAway(t *testing.T) {
	s := newTestStore(t)
	enqueueAnalyzed(t, s, "acme/widget", "v1", "2026-09-01T00:00:00Z")
	enqueueAnalyzed(t, s, "acme/widget", "v2", "2026-09-04T00:00:00Z")
	nodes := []GraphNode{{Key: "acme/widget", Kind: "action", IsExternal: true, Owner: "acme"}}
	edges := []GraphEdge{{Source: "acme/widget", Target: "acme/other"}}

	got, out, err := s.splitNodesByRef(nodes, edges)
	if err != nil {
		t.Fatalf("splitNodesByRef: %v", err)
	}

	keys := map[string]bool{}
	for _, n := range got {
		keys[n.Key] = true
	}
	for _, e := range out {
		if !keys[e.Source] {
			t.Errorf("edge leaves %q, which is not a node", e.Source)
		}
	}
}

func splitKeys(t *testing.T, s *Store, nodes []GraphNode, edges []GraphEdge) map[string]bool {
	t.Helper()
	out, _, err := s.splitNodesByRef(append([]GraphNode(nil), nodes...), append([]GraphEdge(nil), edges...))
	if err != nil {
		t.Fatalf("splitNodesByRef: %v", err)
	}
	keys := map[string]bool{}
	for _, n := range out {
		keys[n.Key] = true
	}
	return keys
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
