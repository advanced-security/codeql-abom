package store

import (
	"strings"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// A bounded inventory must drive from the pairs, not test every node.
//
// Measured 2026-09-15 on the crawled organization: written as a correlated subquery in the WHERE,
// the plan is `SCAN abom_node` with the subquery evaluated per node — 11,235 evaluations to return
// 38 rows. Written as a list subquery over `abom_node_reach`, the planner uses the covering index
// on `repository_row_id` and reduces the outer table to a primary key lookup, because the pair set
// for one grouping is small and the node table is not.
//
// This asserts the plan rather than the clock: on a corpus this size both forms answer quickly, so
// a stopwatch would pass on the shape that does not survive an enterprise estate.
func TestABoundedInventoryDrivesFromTheReachPairsRatherThanScanningNodes(t *testing.T) {
	s := newTestStore(t)

	scope := RepositoryScope{Include: []viewspec.PropertyMatch{
		{Name: "business_id", Value: "3"}}}
	keep, args, err := s.reachedByScope(scope)
	if err != nil {
		t.Fatalf("building the row filter: %v", err)
	}
	if keep == "" {
		t.Fatal("an asked-for scope produced no row filter")
	}

	query := `SELECT n.node_key FROM abom_node_live n WHERE n.node_kind = 'action'` + keep
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explaining the bounded inventory query: %v", err)
	}
	defer rows.Close()

	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")

	if strings.Contains(joined, "CORRELATED") {
		t.Errorf("the bounded filter is evaluated per node:\n%s", joined)
	}
	if !strings.Contains(joined, "idx_node_reach_repository") {
		t.Errorf("the reach index is unused, so the filter scans the pair table:\n%s", joined)
	}
}
