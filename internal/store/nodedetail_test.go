package store

import "testing"

// Anything reachable only through a dependency that was never expanded cannot appear in any walk,
// so the count of what is still pending travels with every answer about who uses something.
func TestUnanalyzedDependenciesCountsWhatTheCrawlHasNotFinished(t *testing.T) {
	s := subgraphFixture(t)
	before, err := s.UnanalyzedDependencies()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, depth, status, score, enqueued_at)
		VALUES ('some/unexpanded', 1, 'pending', 1.0, CURRENT_TIMESTAMP),
		       ('some/finished', 1, 'done', 1.0, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seeding frontier: %v", err)
	}
	after, err := s.UnanalyzedDependencies()
	if err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Errorf("unanalyzed went %d -> %d after one pending and one done row, want +1", before, after)
	}
}

// What a node uses is folded to the container a reader recognizes, and an unknown node is a fair
// question with an empty answer rather than a null one.
func TestANodesDetailListsWhatItUsesAndNothingForAnUnknownNode(t *testing.T) {
	s := subgraphFixture(t)
	if _, err := s.NodeDetailOf("nobody/nothing"); err == nil {
		t.Error("an unknown node answered as though it existed")
	}
	uses, err := s.nodeUses("nobody/nothing")
	if err != nil {
		t.Fatal(err)
	}
	if uses == nil {
		t.Error("an unknown node's uses are nil; an empty list is the contract")
	}
}
