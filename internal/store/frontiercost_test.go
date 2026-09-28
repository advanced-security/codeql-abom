package store

import (
	"strings"
	"testing"
)

// The discovery queue must not walk the edge table once per queue row.
//
// LIMIT does not bound the work because ORDER BY evaluates the select list before discarding rows.
// Assert the plan rather than the clock. `selectSuites` stays correlated on purpose, so this
// checks specifically for correlated edge-table reads.
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
