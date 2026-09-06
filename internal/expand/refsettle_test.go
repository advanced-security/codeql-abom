package expand

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/frontier"
)

// A dependency claimed at a reference must come out of the queue when its analysis finishes.
//
// The queue is keyed `(node_key, ref)` and a batch is looked up by `owner/repo@ref`, but the
// prepared job carried `owner/repo`. The lookup missed, `consume` ran against a zero-value batch
// holding no items, and nothing was ever settled. On the real corpus that left 40 rows `running`
// for good, froze `done` at 1,169, and attributed 125 query runs and 203 edges to a repository
// literally named "/" — the zero batch's own `full()`.
//
// It only shows with a reference, because `key()` and `full()` are the same string without one.
func TestADependencyAnalyzedAtARefIsSettledRatherThanLeftRunning(t *testing.T) {
	h := newHarness(t)
	h.enqueue(frontier.Item{
		NodeKey: "actions/checkout", Ref: "v4",
		Owner: "actions", Repo: "checkout", Depth: 1,
	})

	stats := h.run(10)

	if stats.Items != 1 {
		t.Errorf("settled %d dependencies, want 1", stats.Items)
	}
	items, err := h.queue.Claim(10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// Only the pair that was claimed. The fixture legitimately discovers actions/checkout at
	// other references, and those being claimable is the crawl working.
	for _, it := range items {
		if it.NodeKey == "actions/checkout" && it.Ref == "v4" {
			t.Errorf("%s@%s is still claimable, so it was never taken out of the queue",
				it.NodeKey, it.Ref)
		}
	}
}

// A completed row must record the reference it actually analyzed.
//
// `settle` hardcoded `Ref: "HEAD"`, which erases the answer the per-reference queue exists to
// give and breaks the drift loop underneath it: `Resolution.Mutable()` decides whether a
// reference can move by comparing it to the commit, so a row pinned to a SHA was recorded as
// HEAD, compared unequal, and rechecked forever despite being immutable by construction.
func TestACompletedRowRecordsTheReferenceItAnalyzed(t *testing.T) {
	const sha = "08c6903cd8c0fde910a37f88322edcfb5dd907a8"
	h := newHarness(t)
	h.enqueue(frontier.Item{
		NodeKey: "actions/checkout", Ref: sha,
		Owner: "actions", Repo: "checkout", Depth: 1,
	})

	h.run(10)

	var analyzed string
	if err := h.expander.Store.DB().QueryRow(
		`SELECT COALESCE(analyzed_ref, '') FROM abom_frontier
		 WHERE node_key = 'actions/checkout' AND ref = ?`, sha).Scan(&analyzed); err != nil {
		t.Fatalf("reading the analyzed ref: %v", err)
	}
	if analyzed != sha {
		t.Errorf("analyzed_ref = %q, want the reference that was checked out (%q)", analyzed, sha)
	}
}

// The zero-value batch names itself "/", so a lookup miss does not fail — it invents a
// repository and files real analysis under it.
func TestAnalysisIsNeverAttributedToTheZeroBatch(t *testing.T) {
	h := newHarness(t)
	h.enqueue(frontier.Item{
		NodeKey: "actions/checkout", Ref: "v4",
		Owner: "actions", Repo: "checkout", Depth: 1,
	})

	h.run(10)

	var name string
	err := h.expander.Store.DB().QueryRow(
		`SELECT repository_full_name FROM repository WHERE repository_full_name IN ('/', '')`).
		Scan(&name)
	if err == nil {
		t.Errorf("a repository named %q was created from a batch that was never found", name)
	}
}
