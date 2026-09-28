package store

import (
	"strings"
	"testing"
)

// The queue's sort must name every column that distinguishes a row.
//
// Otherwise LIMIT selects an arbitrary subset when multiple references of one action tie. Assert
// the ORDER BY itself rather than an observed sequence, which can be stable by accident.
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
