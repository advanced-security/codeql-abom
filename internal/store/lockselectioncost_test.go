package store

import (
	"strings"
	"testing"
)

// Discovery asks this once per repository it settles, and every reader that says what a lock
// selected asks it about the whole corpus. The lock tables hold every entry of every locked
// commit read, so a scan of any of them is a cost that grows with the corpus rather than with
// the question, and each has to be reached by its key.
func TestLockSelectionReachesEveryLockTableByItsKey(t *testing.T) {
	s := newTestStore(t)
	// The test store defers indexes, as a bulk load does; a served or crawling one has them all,
	// and the plan that matters is the one it gets.
	if err := s.CreateIndexes(); err != nil {
		t.Fatalf("CreateIndexes: %v", err)
	}

	for name, c := range map[string]struct {
		scope lockScope
		args  []any
	}{
		"one repository": {oneRepository, []any{"octo/tool", "octo", "tool",
			"0123456789abcdef0123456789abcdef01234567"}},
		"every repository": {everyRepository, nil},
	} {
		plan := explainSteps(t, s, lockGovernedQuery(c.scope), c.args...)
		// The plan names each table by the alias the query gives it.
		for _, step := range plan {
			for _, alias := range []string{"w", "d", "parent", "u"} {
				if step == "SCAN "+alias || strings.HasPrefix(step, "SCAN "+alias+" ") {
					t.Errorf("%s: lock table %s is scanned:\n\t%s", name, alias,
						strings.Join(plan, "\n\t"))
				}
			}
			// The walk through a repository's own composites goes outward from what a workflow
			// reaches. Starting from every internal node the corpus holds was measured at eight
			// times the cost on the crawled organization, per settle.
			if strings.Contains(step, "idx_node_external") {
				t.Errorf("%s: the composite walk starts from every node of one kind:\n\t%s",
					name, strings.Join(plan, "\n\t"))
			}
		}
	}
}

func explainSteps(t *testing.T, s *Store, query string, args ...any) []string {
	t.Helper()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explaining: %v", err)
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
	return plan
}
