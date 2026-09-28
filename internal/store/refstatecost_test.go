package store

import (
	"strings"
	"testing"
)

// The graph's reference states must not walk the edge table once per queue row.
//
// This is `usedByCount` a second time, in a second place. The plan is asserted rather than the
// clock: a stopwatch flakes, and it would also pass on a corpus whose edge table happens to be
// small. The cost is structural, so the plan is where it can be seen.
func TestTheReferenceStateQueryDoesNotWalkTheEdgeTablePerQueueRow(t *testing.T) {
	s := newTestStore(t)

	rows, err := s.db.Query("EXPLAIN QUERY PLAN " + refStatesQuery)
	if err != nil {
		t.Fatalf("explaining the reference state query: %v", err)
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

	// A correlated subquery reading the edge table is the per-row walk. Reading it once to build
	// an aggregate is a scan, and is the fix rather than the defect.
	if hasCorrelatedEdgeRead(plan) {
		t.Errorf("the reference state query still walks the edge table per queue row:\n\t%s",
			strings.Join(plan, "\n\t"))
	}
}

// The queue view and the graph must not disagree about how many repositories name a version.
//
// Both answer "who runs this exact version" from the same edges, and they now do it through
// separate SQL. `ownedFullName` exists because the tier, the queue view and the node users had
// drifted into three answers to one question; this is the guard that stops the same happening to
// the count, and it is why both read `usersPerRefRollup` rather than each carrying a copy.
func TestTheQueueAndTheGraphAgreeOnHowManyRepositoriesNameAVersion(t *testing.T) {
	s := newTestStore(t)
	seedDependency(t, s, "octo-org/app", "actions/checkout", "v4", 3)
	seedDependency(t, s, "octo-org/other", "actions/checkout", "v4", 5)
	// The queue row has to name the same reference the edges do, or the two sides never meet and
	// the comparison passes by never happening.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, score, enqueued_at)
		VALUES ('actions/checkout', 'v4', 'actions', 'checkout', 1, 'pending', 1,
		        '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("queuing the dependency: %v", err)
	}

	items, err := s.FrontierSnapshot("", 500)
	if err != nil {
		t.Fatalf("FrontierSnapshot: %v", err)
	}
	states, err := s.refStates()
	if err != nil {
		t.Fatalf("refStates: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("the fixture queued nothing, so neither side was exercised")
	}

	checked := 0
	for _, it := range items {
		st, ok := states[[2]string{it.NodeKey, it.Ref}]
		if !ok {
			continue
		}
		checked++
		if st.dependent != it.UsedByCount {
			t.Errorf("%s@%s: the graph says %d repositories name it and the queue says %d",
				it.NodeKey, it.Ref, st.dependent, it.UsedByCount)
		}
		// Agreeing on nothing is not agreement. Two counts that are both zero pass this test
		// while proving neither side reads the edges, which is how a green test hides a defect.
		if it.UsedByCount == 0 {
			t.Errorf("%s@%s: both sides answered 0, so the comparison proved nothing",
				it.NodeKey, it.Ref)
		}
	}
	if checked == 0 {
		t.Fatal("no queue row matched a reference state, so the two were never compared")
	}
}
