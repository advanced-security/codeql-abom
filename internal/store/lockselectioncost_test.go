package store

import (
	"strings"
	"testing"
)

// Discovery asks this once per repository it settles, and the lock tables hold every entry of
// every locked commit the corpus has read. A scan of any of them per settle is a cost that grows
// with the corpus rather than with the repository, so each has to be reached by its key.
func TestLockSelectionReachesEveryLockTableByItsKey(t *testing.T) {
	s := newTestStore(t)
	// The test store defers indexes, as a bulk load does; a served or crawling one has them all,
	// and the plan that matters is the one it gets.
	if err := s.CreateIndexes(); err != nil {
		t.Fatalf("CreateIndexes: %v", err)
	}

	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+lockSelectionQuery,
		"octo/tool", "octo", "tool", "0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatalf("explaining the lock selection query: %v", err)
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

	t.Logf("plan:\n\t%s", strings.Join(plan, "\n\t"))
	// The walk through the repository's own composites goes outward from what a workflow
	// reaches. Starting from every internal node the corpus holds was measured at eight times
	// the cost on the crawled organization, per settle.
	for _, step := range plan {
		if strings.Contains(step, "idx_node_external") {
			t.Errorf("the composite walk starts from every node of one kind:\n\t%s",
				strings.Join(plan, "\n\t"))
		}
	}
	// The plan names each table by the alias the query gives it.
	for _, step := range plan {
		for _, alias := range []string{"w", "parent", "u", "child"} {
			if step == "SCAN "+alias || strings.HasPrefix(step, "SCAN "+alias+" ") {
				t.Errorf("lock table %s is scanned:\n\t%s", alias, strings.Join(plan, "\n\t"))
			}
		}
	}
}
