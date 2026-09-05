package expand

import (
	"context"
	"strings"
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

func (h *harness) requeueDirect() int {
	h.t.Helper()
	n, err := h.store.RequeueTier(store.TierDirect)
	if err != nil {
		h.t.Fatalf("RequeueTier: %v", err)
	}
	return n
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
func TestAQueryNotYetOnRecordIsStillAnalyzed(t *testing.T) {
	h := drainedHarness(t)
	built := h.databasesBuilt()

	requeued := h.requeueDirect()
	h.runQuery(100, "abom/edges-amended")

	if again := h.databasesBuilt() - built; again != requeued {
		t.Errorf("a query not on record built %d databases, want %d", again, requeued)
	}
}

// The checkout is not avoidable and must not be claimed as avoided: the run key includes the
// commit, and the commit is only known once the source is in hand. Skipping on the frontier's
// last known sha would skip precisely when a ref had moved.
func TestASkipStillResolvesTheCommitItSkippedOn(t *testing.T) {
	h := drainedHarness(t)
	h.mu.Lock()
	h.fetched = nil
	h.mu.Unlock()

	h.requeueDirect()
	h.run(100)

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, full := range h.fetched {
		if strings.Contains(full, "gradle/actions") {
			return
		}
	}
	t.Error("the skipping pass never fetched, so it cannot know the commit it skipped on")
}
