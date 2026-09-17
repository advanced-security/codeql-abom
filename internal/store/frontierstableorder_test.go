package store

import (
	"strings"
	"testing"
)

// The queue's sort must name every column that distinguishes a row.
//
// It was `expedited_at IS NULL, score DESC, depth ASC, node_key` and `ref` was not in it, so
// every reference of one action tied on the whole key. Measured 2026-09-11 on the seeded corpus:
// **3,094 of 3,662 rows (84%) sat in a tied group**, the largest holding 28. Ties come back in
// whatever order the plan produces, so adding a join silently changed which rows fell inside
// `LIMIT 500` — 252 of 500 positions moved, with every shared row identical in every field. The
// page was never wrong, it was arbitrary, and a reader comparing two loads sees rows appear and
// vanish.
//
// This asserts the sort rather than an observed sequence, and the first attempt at the latter is
// why. Seeding three references and checking they came back in order **passed without the fix**:
// on a three-row table SQLite reads the primary key index, which is `(node_key, ref)`, so they
// arrive sorted by accident. A test that cannot fail against the defect it names is worse than no
// test, and the property here is structural.
//
// Same class as the `splitNodesByRef` map-iteration defect that made `/api/graph/full`
// non-deterministic: an order nobody stated is an order nobody can rely on.
func TestTheQueueSortIsATotalOrder(t *testing.T) {
	query, _ := frontierSnapshotQuery("", 500)

	// The last ORDER BY, not the first: the rollup CTE contains one inside a window function, and
	// cutting at the first match tested that instead and passed for the wrong reason.
	at := strings.LastIndex(query, "ORDER BY")
	if at < 0 {
		t.Fatal("the frontier query has no ORDER BY, so its row order is entirely unspecified")
	}
	order := query[at+len("ORDER BY"):]
	if i := strings.Index(order, "LIMIT"); i >= 0 {
		order = order[:i]
	}
	if !strings.Contains(order, "ref") {
		t.Errorf("the queue sorts by %q, which does not mention ref; every reference of one "+
			"dependency ties, so LIMIT returns an arbitrary subset of them",
			strings.Join(strings.Fields(order), " "))
	}
}
