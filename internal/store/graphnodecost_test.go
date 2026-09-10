package store

import (
	"strings"
	"testing"
)

// The graph node query must not re-read the frontier once per node.
//
// Measured 2026-09-10 on the seeded corpus: `frontierMatch` matches on `f.node_key = n.node_key`
// OR on `LOWER(f.node_key)`, and a function on the column means no index can serve the second
// branch, so SQLite scans the whole frontier for every node — five times over, once per frontier
// column. One of those five subqueries cost 17.7 s against 0.045 s with the OR branch removed,
// and the whole endpoint took 21 s for 12,550 nodes.
//
// This asserts the plan rather than the clock, because a stopwatch here would flake and would
// also pass on a corpus whose frontier happens to be small. The cost has to be structural.
func TestTheGraphNodeQueryDoesNotRereadTheFrontierPerNode(t *testing.T) {
	s := newTestStore(t)

	query, args := graphNodesQuery(collapsedDisplay, 0, FullGraphOptions{})

	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explaining the graph node query: %v", err)
	}
	defer rows.Close()

	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scanning the query plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the query plan: %v", err)
	}

	for _, step := range plan {
		// A correlated scan is the per-node re-read. A single ungrouped pass over the frontier
		// to build an aggregate is a scan too, and is the fix rather than the defect, so the
		// correlation is what this looks for.
		if strings.Contains(step, "CORRELATED") {
			t.Errorf("the query plan still runs a correlated subquery per node:\n\t%s",
				strings.Join(plan, "\n\t"))
			break
		}
	}
}
