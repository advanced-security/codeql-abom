package store

import "testing"

// prunedThenNamedAgain prunes stranger/thing and its edge, records the generation a client would
// hold, and has a later run name both again.
func prunedThenNamedAgain(t *testing.T) (*Store, int64) {
	t.Helper()
	s := pruneFixture(t)
	if _, err := s.MarkOutsideClosure([]string{"octo-org/app"}); err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}
	if contains(liveNodes(t, s), "stranger/thing") {
		t.Fatal("the prune left stranger/thing live, so nothing below is a revival")
	}
	seen, err := s.RecordGraphGeneration(GraphRebuild{})
	if err != nil {
		t.Fatal(err)
	}

	stranger, err := s.UpsertRepository("stranger/repo", "")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, 0)
	add := adder(t, w)
	add(Node{Key: "stranger/repo/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: stranger})
	add(Node{Key: "stranger/thing", Kind: "action", Owner: "stranger", Repo: "thing", IsExternal: true})
	if err := w.AddEdge(Edge{Key: "out", SrcKey: "stranger/repo/.github/workflows/ci.yml",
		DstKey: "stranger/thing", Kind: "step-uses-action", RepositoryID: stranger,
		FilePath: ".github/workflows/ci.yml", StartLine: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	return s, seen
}

// A pruned node a later run names again is in the world again, and the graph says so. Left removed,
// the new edge was live and its endpoint was not: 100 such edges on the crawled organization.
func TestANodeARunNamesAgainComesBackWithItsEdge(t *testing.T) {
	s, _ := prunedThenNamedAgain(t)

	if !contains(liveNodes(t, s), "stranger/thing") {
		t.Error("stranger/thing was named again and is still removed")
	}
	if n := countPrune(t, s, `SELECT COUNT(*) FROM abom_edge WHERE edge_key = 'out' AND `+LiveEdge); n != 1 {
		t.Errorf("the edge observed again is live %d times, want 1", n)
	}
	if n := countPrune(t, s, `SELECT COUNT(*) FROM abom_edge_live e
		JOIN abom_node d ON d.row_id IN (e.src_node_row_id, e.dst_node_row_id)
		WHERE d.removed_at IS NOT NULL`); n != 0 {
		t.Errorf("%d live edges name a removed node", n)
	}
}

// A client that saw the removal holds a picture without the node, and a delta of row ids past its
// watermark would never bring it back; so it is told to reload, as after a compaction.
func TestAPageThatSawTheRemovalReloadsWhenTheNodeComesBack(t *testing.T) {
	s, seen := prunedThenNamedAgain(t)

	delta, err := s.GraphDelta(seen, FullGraphOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !delta.Reset {
		t.Errorf("a page that saw stranger/thing removed was not told it came back: %+v", delta)
	}
}

// A database that already holds live edges under removed nodes is repaired when it is opened, so
// the inconsistency does not wait for each node to be named once more.
func TestOpeningADatabaseBringsBackWhatALiveEdgeNames(t *testing.T) {
	s := pruneFixture(t)
	if _, err := s.DB().Exec(`UPDATE abom_node SET removed_at = '2026-09-22T00:00:00Z',
		removed_generation = 1 WHERE node_key = 'stranger/thing'`); err != nil {
		t.Fatal(err)
	}

	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}

	if !contains(liveNodes(t, s), "stranger/thing") {
		t.Error("a live edge names stranger/thing and it is still removed after opening")
	}
}

// A node a run emits with nothing pointing at it, a workflow with no `uses:` at all, still exists.
func TestANodeARunNamesWithNoEdgeComesBackToo(t *testing.T) {
	s := pruneFixture(t)
	if _, err := s.MarkOutsideClosure([]string{"octo-org/app"}); err != nil {
		t.Fatal(err)
	}
	if contains(liveNodes(t, s), "stranger/thing") {
		t.Fatal("the prune left stranger/thing live, so nothing below is a revival")
	}
	w := NewWriter(s, 0)
	adder(t, w)(Node{Key: "stranger/thing", Kind: "action", Owner: "stranger", Repo: "thing", IsExternal: true})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	if !contains(liveNodes(t, s), "stranger/thing") {
		t.Error("stranger/thing was named again with no edge and is still removed")
	}
}
