package store

import (
	"strings"
	"testing"
)

// The discovery queue must not walk the edge table once per queue row.
//
// Measured 2026-09-11 on the seeded corpus: the sixteen plain columns of this query take 0.01 s
// over all 3,662 rows, and the query as shipped took 1.65 s. The difference is `selectUsedBy`,
// whose three correlated subqueries each re-walk `abom_edge_live` joined to `abom_node_live` for
// the same (node_key, ref) pair — three times per row, 3,662 times over. A GROUP BY rollup does
// the same work once, in 0.06 s, and agrees on every row.
//
// `LIMIT 500` does not rescue it: the ORDER BY is a temp b-tree, so the select list is evaluated
// for every row before the limit can apply. The Discovery page looks bounded and is not.
//
// This asserts the plan rather than the clock. A stopwatch would flake, and would also pass on a
// corpus whose edge table happens to be small — but the cost is structural, so the plan is where
// it can be seen. `selectSuites` stays correlated on purpose: it is served by
// `idx_query_run_repo` and measured +0.04 s, so this looks for the edge walk specifically rather
// than banning correlation outright.
func TestTheFrontierQueryDoesNotWalkTheEdgeTablePerQueueRow(t *testing.T) {
	s := newTestStore(t)

	query, args := frontierSnapshotQuery("", 500)

	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explaining the frontier query: %v", err)
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

	// A correlated subquery reading the edge table is the per-row walk. The rollup reads the same
	// table once to build an aggregate, which is a scan and is the fix, so correlation is what
	// separates the two.
	if hasCorrelatedEdgeRead(plan) {
		t.Errorf("the frontier query still walks the edge table per queue row:\n\t%s",
			strings.Join(plan, "\n\t"))
	}
}

func hasCorrelatedEdgeRead(plan []string) bool {
	correlated := false
	for _, step := range plan {
		if strings.Contains(step, "CORRELATED") {
			correlated = true
		}
		words := strings.Fields(step)
		if correlated && len(words) > 1 && (words[0] == "SCAN" || words[0] == "SEARCH") &&
			(words[1] == "abom_edge" || words[1] == "e") {
			return true
		}
	}
	return false
}

func TestTheCostGuardDistinguishesAnEdgeWalkFromASightingLookup(t *testing.T) {
	for _, example := range []struct {
		plan []string
		bad  bool
	}{
		{[]string{"SCAN abom_edge", "CORRELATED SCALAR SUBQUERY", "SEARCH q USING INTEGER PRIMARY KEY"}, false},
		{[]string{"CORRELATED SCALAR SUBQUERY", "SCAN abom_edge"}, true},
		{[]string{"CORRELATED SCALAR SUBQUERY", "SEARCH e USING INDEX idx_edge_dst"}, true},
		{[]string{"CORRELATED SCALAR SUBQUERY", "SEARCH s USING COVERING INDEX sqlite_autoindex_abom_edge_sighting_1 (edge_row_id=?)"}, false},
	} {
		if got := hasCorrelatedEdgeRead(example.plan); got != example.bad {
			t.Errorf("plan %v: per-row edge walk = %v, want %v", example.plan, got, example.bad)
		}
	}
}
