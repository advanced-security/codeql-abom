package store

import (
	"strings"
	"testing"
)

// Ordering by findings looks each row's worst finding up by node. A pre-aggregated join was
// measured on the crawled organization (2,069 workflows, 11,968 finding-node rows): it scanned
// every finding-node row, including those of nodes the unit never lists, and ran in 24 ms against
// the lookup's 10 ms. Asserted as a plan, since a small corpus hides the difference.
func TestSQLiteOrderingByFindingsLooksUpEachRowRatherThanScanningFindings(t *testing.T) {
	s := newTestStore(t)
	// The test store defers indexes as a bulk load does; a serving store has them.
	if err := s.CreateIndexes(); err != nil {
		t.Fatalf("CreateIndexes: %v", err)
	}

	for name, query := range map[string]string{
		"workflows": `SELECT n.node_key, 0 AS findings FROM abom_node_live n
			LEFT JOIN repository r ON r.row_id = n.repository_row_id
			WHERE n.node_kind = 'workflow'` + unitOrdering("findings", "", " DESC"),
		"inventory": func() string {
			ordering, _ := severityOrdering(AnalysisModeRegular, " DESC", "n.row_id", nil)
			return `SELECT n.node_key FROM abom_node_live n WHERE n.node_kind = 'action'` + ordering
		}(),
	} {
		plan := strings.Join(explainPlan(t, s, query), "\n")
		if strings.Contains(plan, "SCAN fn") || strings.Contains(plan, "SCAN abom_finding_node") {
			t.Errorf("%s: ordering by findings scans the finding-node table:\n%s", name, plan)
		}
		if !strings.Contains(plan, "SEARCH fn USING INDEX idx_finding_node") {
			t.Errorf("%s: the worst finding is not looked up by node:\n%s", name, plan)
		}
	}
}
