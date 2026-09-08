package expand

import (
	"context"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// runQuery is h.run with the query identity spelled out, so a test can change what is being run
// and watch the ledger decide.
func (h *harness) runQuery(limit int, queryID string) Stats {
	h.t.Helper()
	stats, err := h.expander.Run(context.Background(), Options{
		QueryPath: "AbomEdges.ql", QueryID: queryID,
		WorkDir: h.t.TempDir(), Limit: limit,
	})
	if err != nil {
		h.t.Fatalf("Run: %v", err)
	}
	return stats
}

// drain analyzes until nothing is pending, because one pass enqueues the dependencies it finds
// and a comparison against a queue still filling up measures the discoveries, not the skip.
func (h *harness) drain() {
	h.t.Helper()
	for range 10 {
		if h.pending() == 0 {
			return
		}
		h.run(100)
	}
	h.t.Fatalf("the queue still holds %d pending items after 10 passes", h.pending())
}

func (h *harness) pending() int {
	h.t.Helper()
	var n int
	if err := h.store.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_frontier WHERE status = 'pending'`).Scan(&n); err != nil {
		h.t.Fatalf("counting pending: %v", err)
	}
	return n
}

// requeueDirect puts the analyzed rows back, whichever tier holds them.
//
// It requeued only `direct` until 2026-09-07, when a declared root became owned-tier and this
// started finding nothing. Every caller wants "put back what was analyzed", not one tier.
func (h *harness) requeueDirect() int {
	h.t.Helper()
	total := 0
	for _, tier := range []string{store.TierOwned, store.TierDirect, store.TierTransitive} {
		n, err := h.store.RequeueTier(tier)
		if err != nil {
			h.t.Fatalf("RequeueTier(%s): %v", tier, err)
		}
		total += n
	}
	return total
}

// databasesBuilt counts CodeQL database builds, which is the expensive step the skip exists to
// avoid. The checkout still happens: the commit is only known once the source is in hand.
func (h *harness) databasesBuilt() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.built)
}

func countRows(t *testing.T, s *store.Store, query string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// drainedHarness analyzes everything reachable from one seed, so what follows measures
// re-analysis rather than a queue still filling up.
func drainedHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()
	return h
}

// CodeQL is deterministic, so the same query, from the same pack at the same digest, on the same
// CLI, against the same commit cannot produce a different answer. Recognizing that is the whole
// reason the run identity is recorded: re-analyzing a tier should cost only what actually moved.
func TestATierAlreadyOnRecordIsNotAnalyzedAgain(t *testing.T) {
	h := drainedHarness(t)
	built := h.databasesBuilt()

	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued, so the skip would not be exercised")
	}
	h.run(100)

	if again := h.databasesBuilt() - built; again != 0 {
		t.Errorf("re-analysis built %d databases, want none", again)
	}
}

// A skip that does not settle the item hands it straight back to the next Claim and the crawl
// spins on it forever. Only consume completes items, which is why the skip cannot simply be an
// error returned from the parallel phase.
func TestASkippedRepositoryStillLeavesTheQueue(t *testing.T) {
	h := drainedHarness(t)
	done := countRows(t, h.store, `SELECT COUNT(*) FROM abom_frontier WHERE status = 'done'`)

	requeued := h.requeueDirect()
	h.run(100)

	got := countRows(t, h.store, `SELECT COUNT(*) FROM abom_frontier WHERE status = 'done'`)
	if got != done {
		t.Errorf("%d of %d items are done after re-analyzing %d, want all of them",
			got, done, requeued)
	}
	if p := h.pending(); p != 0 {
		t.Errorf("%d items are still pending after a skipping pass", p)
	}
	status, sha, _ := statusOf(t, h.store, "gradle/actions/setup-gradle")
	if status != frontier.StatusDone || sha != "sha-actions" {
		t.Errorf("the skipped item is %q at %q, want done at sha-actions", status, sha)
	}
}

func TestASkippingPassIngestsNothing(t *testing.T) {
	h := drainedHarness(t)
	before := countRows(t, h.store, `SELECT COUNT(*) FROM abom_edge`)

	h.requeueDirect()
	stats := h.run(100)

	if stats.Edges != 0 {
		t.Errorf("a skipping pass ingested %d edge rows, want none", stats.Edges)
	}
	if after := countRows(t, h.store, `SELECT COUNT(*) FROM abom_edge`); after != before {
		t.Errorf("edges went %d -> %d across a skipping pass, want no change", before, after)
	}
}

// The skip is keyed on identity, not on "this repository was seen". Amending a query has to
// defeat it, or a fix would never reach anything already analyzed.
//
// Asserted on the ledger rather than on database builds: two references of one repository can
// share indexed content, and the second is then settled by content equivalence without a
// rebuild. That is a different mechanism working, not this one failing.
func TestAQueryNotYetOnRecordIsStillAnalyzed(t *testing.T) {
	h := drainedHarness(t)

	if requeued := h.requeueDirect(); requeued == 0 {
		t.Fatal("nothing was requeued, so the skip would not be exercised")
	}
	h.runQuery(100, "abom/edges-amended")

	if n := countRows(t, h.store,
		`SELECT COUNT(*) FROM abom_query_run WHERE query_id = 'abom/edges-amended'`); n == 0 {
		t.Error("a query not on record never ran, so amending one would reach nothing")
	}
}

// A skip must still record the commit it skipped on, or the row claims an analysis of a commit
// nobody established.
//
// This used to assert a fetch, on the reasoning that the commit is only knowable once the source
// is in hand. That is no longer true: a reference resolves remotely, so the commit is known
// before the clone and the clone is skipped with it. The guarantee is unchanged and asserted
// directly. Deciding from the frontier's *stored* sha would still be wrong — it would skip
// precisely when a ref had moved — and is not what happens, because the probe asks the remote
// what the reference points at now.
func TestASkipStillResolvesTheCommitItSkippedOn(t *testing.T) {
	h := drainedHarness(t)

	h.requeueDirect()
	h.run(100)

	status, sha, _ := statusOf(t, h.store, "gradle/actions/setup-gradle")
	if status != "done" {
		t.Errorf("status = %q, want done", status)
	}
	if sha != "sha-actions" {
		t.Errorf("resolved_sha = %q, want the commit the skip was decided on", sha)
	}
}
