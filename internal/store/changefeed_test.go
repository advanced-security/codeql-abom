package store

import (
	"slices"
	"testing"
)

func TestReachCoverageDoesNotTreatALateLowerIDAsMeasured(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO abom_node (row_id, node_key, node_kind)
		VALUES (1000, 'higher/measured', 'action')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordGraphGeneration(GraphRebuild{ReachRebuilt: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO abom_node (row_id, node_key, node_kind)
		VALUES (100, 'lower/unmeasured', 'action')`); err != nil {
		t.Fatal(err)
	}
	graph, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		if node.Key == "lower/unmeasured" && node.ReachKnown {
			t.Fatal("a node committed after the measurement was reported as measured")
		}
	}
}

func TestDeltaIncludesARowCommittedAfterACursorWithALowerID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO abom_node (row_id, node_key, node_kind)
		VALUES (1000, 'higher/first', 'action')`); err != nil {
		t.Fatal(err)
	}
	cursor, err := s.RecordGraphGeneration(GraphRebuild{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO abom_node (row_id, node_key, node_kind)
		VALUES (100, 'lower/later', 'action')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordGraphGeneration(GraphRebuild{}); err != nil {
		t.Fatal(err)
	}
	delta, err := s.GraphDelta(cursor, FullGraphOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if delta.Reset || !slices.ContainsFunc(delta.Nodes, func(n GraphNode) bool {
		return n.Key == "lower/later"
	}) {
		t.Fatalf("a later commit was lost behind a row-id watermark: %+v", delta)
	}
}

func TestDeltaCarriesUpdatesToAnExistingDeclaration(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, 0)
	repo := seedGraph(t, s, w)
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	cursor, err := s.RecordGraphGeneration(GraphRebuild{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE abom_edge SET ref_value = 'changed'
		WHERE repository_row_id = ?`, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordGraphGeneration(GraphRebuild{}); err != nil {
		t.Fatal(err)
	}
	delta, err := s.GraphDelta(cursor, FullGraphOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(delta.Edges, func(e GraphEdge) bool {
		return slices.Contains(e.Refs, "changed")
	}) {
		t.Fatal("updating an existing declaration did not reach the delta")
	}
}
