package store

import (
	"strings"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// A bounded inventory must drive from reach pairs, not test every node.
// This asserts the plan rather than the clock so small corpora cannot hide the shape.
func TestABoundedInventoryDrivesFromTheReachPairsRatherThanScanningNodes(t *testing.T) {
	s := newTestStore(t)

	scope := RepositoryScope{Include: []viewspec.PropertyMatch{
		{Name: "business_id", Value: "3"}}}
	in, err := s.reachInScope(scope)
	if err != nil {
		t.Fatalf("building the row filter: %v", err)
	}
	keep, args := in.keep()
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
