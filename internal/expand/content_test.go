package expand

import (
	"testing"
)

// An action's release moves its tag over a rebuilt `dist/` and touches nothing the extractor
// reads. The commit is new, so the run key is new and the ledger holds nothing for it — but the
// database would be built from byte-identical sources, and CodeQL is deterministic. Rebuilding
// pays the most expensive step of a repository to learn what is already known.
func TestAMovedRefWithUnchangedContentIsNotRebuilt(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued, so the content check would not be exercised")
	}
	built := h.databasesBuilt()

	// Everything moved, so the pre-check finds nothing on record and the fetch goes ahead.
	h.probe.moveEverythingTo("released-sha")
	h.run(100)

	if again := h.databasesBuilt() - built; again != 0 {
		t.Errorf("a release that changed nothing indexed built %d databases, want none", again)
	}
}

// The mirror image, and the one that must not be waived: content that moved is content nobody
// has analyzed, whatever the ledger holds for its predecessor.
func TestAMovedRefWithChangedContentIsRebuilt(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued")
	}
	built := h.databasesBuilt()

	h.probe.moveEverythingTo("released-sha")
	h.probe.rewriteContent("name: ci\non: pull_request\n")
	h.run(100)

	if again := h.databasesBuilt() - built; again == 0 {
		t.Error("changed content was settled without being analyzed")
	}
}

// Equivalence stands in for the commit and for nothing else. A different query against the same
// content has not run, and the content check must not be the thing that says otherwise.
func TestUnchangedContentDoesNotWaiveADifferentQuery(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued")
	}
	built := h.databasesBuilt()

	h.probe.moveEverythingTo("released-sha")
	h.runQuery(100, "abom/edges-v2")

	if again := h.databasesBuilt() - built; again == 0 {
		t.Error("a query that had never run was skipped because the content was familiar")
	}
}
