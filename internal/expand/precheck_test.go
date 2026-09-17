package expand

import (
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// A repository whose whole plan is already on record is settled without being analyzed, but the
// checkout in front of that decision was still paid because the commit was only knowable once
// the source was in hand. A ref resolves remotely for a fraction of a clone, so the commit is
// knowable first — and on a tier requeue, which puts back everything including what is current,
// that is the whole cost of the round.
func TestARepositoryAlreadyOnRecordIsSettledWithoutFetching(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	// One repository rather than every tier: what this protects is the pre-check settling a row
	// whose plan is on record, and a broader requeue also puts back rows whose references the
	// harness's probe resolves differently, which is a different question.
	if _, err := h.store.RequeueRepository("gradle/actions"); err != nil {
		t.Fatalf("RequeueRepository: %v", err)
	}
	if h.pendingCount() == 0 {
		t.Fatal("nothing was requeued, so the pre-check would not be exercised")
	}
	fetched := len(h.fetched)
	h.run(100)

	if again := len(h.fetched) - fetched; again != 0 {
		t.Errorf("re-analysis fetched %d repositories, want none", again)
	}
}

// The trap the ledger's own note describes: deciding from the stored sha alone would skip
// precisely when a ref had moved, which is the case re-analysis exists for. The pre-check asks
// the remote what the ref points at now, so a moved ref is not on record and still gets analyzed.
func TestAMovedRefIsStillFetchedAndAnalyzed(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued")
	}

	// Every ref now points somewhere new, so nothing the ledger holds describes it.
	h.probe.moveEverythingTo("moved-sha")
	fetched := len(h.fetched)
	h.run(100)

	if again := len(h.fetched) - fetched; again == 0 {
		t.Error("a moved ref was settled without being fetched, so drift would go unanalyzed")
	}
}

// A probe that cannot be answered says nothing about whether work is outstanding. Treating that
// as "nothing to do" would settle a dependency nobody looked at.
func TestAnUnanswerableProbeFallsBackToFetching(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued")
	}

	h.probe.failEverything()
	fetched := len(h.fetched)
	h.run(100)

	if again := len(h.fetched) - fetched; again == 0 {
		t.Error("a failed probe settled the batch rather than falling back to a fetch")
	}
}

// A reference that is already a commit cannot move, so asking a remote about it spends a request
// to be told what the row already says.
func TestASHAPinnedReferenceIsNotProbed(t *testing.T) {
	const pinned = "0ad4b8fadaa221de15dcec353f45205ec38ea70b"
	h := newHarness(t)
	h.enqueue(frontier.Item{
		NodeKey: "actions/checkout", Owner: "actions", Repo: "checkout",
		Ref: pinned, Depth: 1,
	})
	h.run(100)
	if _, err := h.store.RequeueTier(store.TierDirect); err != nil {
		t.Fatalf("RequeueTier: %v", err)
	}

	h.run(100)

	// Scoped to the pinned reference: the fixture discovers other dependencies, and those have
	// mutable references that are probed correctly.
	for _, asked := range h.probe.asked {
		if strings.Contains(asked, pinned) {
			t.Errorf("probed %q, which cannot move", asked)
		}
	}
}
