package store

import (
	"strings"
	"testing"
)

// The graph node query must not re-read the frontier once per node.
// The guard asserts the plan shape, not elapsed time, so corpus size cannot hide the cost.
// Correlation inside a materialized CTE is allowed because it runs before the per-node pass.
func TestSQLiteGraphNodeQueryDoesNotRereadTheFrontierPerNode(t *testing.T) {
	for _, shape := range []struct {
		name string
		opts FullGraphOptions
	}{
		{"unfiltered", FullGraphOptions{}},
		// Each scope brings a CTE of its own, and both must be computed before the per-node pass
		// rather than consulted from inside it. Asked for together because a graph narrowed to one
		// group *and* to what the operator owns intersects them, which is when there are two.
		{"scoped and filtered", FullGraphOptions{
			Scope:        ScopeOwned,
			GroupSources: []string{"octo-org/app"},
		}},
		{"supply chain and filtered", FullGraphOptions{
			Scope:        ScopeSupplyChain,
			GroupSources: []string{"octo-org/app"},
		}},
	} {
		t.Run(shape.name, func(t *testing.T) {
			assertNoPerNodeCorrelation(t, shape.opts)
		})
	}
}

func assertNoPerNodeCorrelation(t *testing.T, opts FullGraphOptions) {
	t.Helper()
	s := newTestStore(t)
	if _, err := s.declaredEdgeTable(opts.Mode); err != nil {
		t.Fatal(err)
	}

	query, args := graphNodesQuery(collapsedDisplay, 0, opts)

	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explaining the graph node query: %v", err)
	}
	defer rows.Close()

	type step struct {
		id, parent int
		detail     string
	}
	var plan []step
	for rows.Next() {
		var s step
		var notused int
		if err := rows.Scan(&s.id, &s.parent, &notused, &s.detail); err != nil {
			t.Fatalf("scanning the query plan: %v", err)
		}
		plan = append(plan, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the query plan: %v", err)
	}

	// A materialized CTE is computed once, before the per-node pass reads it, so a correlation
	// inside one costs a pass over that CTE's own rows rather than a pass per node. Parents
	// precede their children in this output, so one forward walk marks every descendant.
	//
	// A closure reaches the plan as an uncorrelated LIST SUBQUERY rather than a MATERIALIZE —
	// SQLite builds it once and hangs a bloom filter off it, which it would not do for something
	// re-evaluated per row. It writes "CORRELATED" on any subquery that depends on the outer row,
	// so the absence of that word is the evidence, not an assumption about the shape.
	precomputed := map[int]bool{}
	var details []string
	for _, s := range plan {
		details = append(details, s.detail)
		if strings.HasPrefix(s.detail, "MATERIALIZE") || strings.HasPrefix(s.detail, "CO-ROUTINE") ||
			strings.HasPrefix(s.detail, "LIST SUBQUERY") || precomputed[s.parent] {
			precomputed[s.id] = true
		}
	}

	for _, s := range plan {
		// A correlated scan in the per-node pass is the re-read. A single ungrouped pass over the
		// frontier to build an aggregate is a scan too, and is the fix rather than the defect, so
		// the correlation is what this looks for.
		if strings.Contains(s.detail, "CORRELATED") && !precomputed[s.id] && !precomputed[s.parent] {
			t.Errorf("the query plan still runs a correlated subquery per node:\n\t%s",
				strings.Join(details, "\n\t"))
			break
		}
	}
}
