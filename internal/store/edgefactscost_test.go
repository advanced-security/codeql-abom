package store

import (
	"strings"
	"testing"
)

// A recursive step that cannot reach a unit's nodes by an index reads every edge for every unit it
// takes: on the crawled organization that was 16.6 s for 759 units with a finding behind them, all
// of it holding the store's one connection, and webhook deliveries waited 21 s to be recorded.
func TestSQLiteEdgeFactClosuresReachAUnitsNodesByIndex(t *testing.T) {
	s := newTestStore(t)
	// As a serving database has them: the test store defers indexes as a bulk load does.
	if err := s.CreateIndexes(); err != nil {
		t.Fatalf("CreateIndexes: %v", err)
	}
	query, args := edgeFactsQuery()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explaining the edge facts: %v", err)
	}
	defer rows.Close()
	type step struct {
		id, parent int
		detail     string
	}
	var plan []step
	for rows.Next() {
		var p step
		var notused int
		if err := rows.Scan(&p.id, &p.parent, &notused, &p.detail); err != nil {
			t.Fatalf("scanning the query plan: %v", err)
		}
		plan = append(plan, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the query plan: %v", err)
	}

	// Parents precede their children, so one forward walk finds everything under a recursive step.
	recursive := map[int]bool{}
	steps := 0
	for _, p := range plan {
		if p.detail == "RECURSIVE STEP" {
			recursive[p.id] = true
			steps++
			continue
		}
		if !recursive[p.parent] {
			continue
		}
		recursive[p.id] = true
		if strings.HasPrefix(p.detail, "SCAN abom_edge") || strings.HasPrefix(p.detail, "SCAN abom_node") {
			t.Errorf("a recursive step reads a whole table for each unit it takes: %s", p.detail)
		}
	}
	if steps != 2 {
		t.Fatalf("found %d recursive steps, want the finding and ownership closures' 2", steps)
	}
}
