package expand

import (
	"testing"
)

// Lock coverage is read from the checkout, and a reference with nothing outstanding never takes
// one: it settles from the ledger alone. That path is the normal one for a database that already
// holds analysis — an upgrade, or a tier requeue that puts back everything including what is
// current — so without this the table stays empty on exactly the repositories already analyzed,
// and "no lockfile anywhere" and "nobody looked" are recorded identically.
func TestARefSettledWithoutFetchingStillRecordsWhatItsLockfileSaid(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	// A database analyzed before coverage was recorded: the ledger is complete and the coverage
	// table is not. Deleting is how that state is reached here; upgrading an existing database
	// is how it is reached in the field.
	if _, err := h.store.DB().Exec(`DELETE FROM abom_lock_coverage`); err != nil {
		t.Fatalf("clearing the recorded coverage: %v", err)
	}

	if _, err := h.store.RequeueRepository("gradle/actions"); err != nil {
		t.Fatalf("RequeueRepository: %v", err)
	}
	if h.pendingCount() == 0 {
		t.Fatal("nothing was requeued, so the pre-check would not be exercised")
	}
	h.run(100)

	var rows int
	if err := h.store.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_lock_coverage WHERE repository = 'gradle/actions'`).
		Scan(&rows); err != nil {
		t.Fatalf("counting the recorded coverage: %v", err)
	}
	if rows == 0 {
		t.Error("the reference settled without reading its lockfile, and nothing recorded that")
	}
}

// The other half, and what keeps the gate above from becoming a second fetch of everything: once
// a commit's coverage is on record, settling it again reads nothing.
func TestACommitWhoseCoverageIsOnRecordStillSettlesWithoutFetching(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	if _, err := h.store.RequeueRepository("gradle/actions"); err != nil {
		t.Fatalf("RequeueRepository: %v", err)
	}
	fetched := len(h.fetched)
	h.run(100)

	if again := len(h.fetched) - fetched; again != 0 {
		t.Errorf("re-analysis fetched %d repositories, want none", again)
	}
}
