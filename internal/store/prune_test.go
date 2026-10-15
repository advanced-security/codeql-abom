package store

import "testing"

// The boundary stops the graph growing; it does nothing about what is already outside.
func TestWhatFallsOutsideTheClosureIsMarkedRemoved(t *testing.T) {
	s := pruneFixture(t)

	report, err := s.MarkOutsideClosure([]string{"octo-org/app"})
	if err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}

	if report.Nodes == 0 || report.Edges == 0 {
		t.Fatalf("nothing was marked: %+v", report)
	}
	if live := liveNodes(t, s); contains(live, "stranger/thing") {
		t.Error("stranger/thing is outside the closure and must not be live")
	}
	if live := liveNodes(t, s); !contains(live, "actions/checkout") {
		t.Error("actions/checkout is inside the closure and must stay")
	}
}

// A dry run has to answer the same question without acting on it. A destructive operation whose
// scope is only visible afterwards is one nobody will run twice.
func TestADryRunReportsWhatWouldGoAndRemovesNothing(t *testing.T) {
	s := pruneFixture(t)
	before := len(liveNodes(t, s))

	report, err := s.ExplainOutsideClosure([]string{"octo-org/app"})
	if err != nil {
		t.Fatalf("ExplainOutsideClosure: %v", err)
	}

	if report.Nodes == 0 {
		t.Error("the explanation reported nothing, so it cannot be checked before acting")
	}
	if after := len(liveNodes(t, s)); after != before {
		t.Errorf("a dry run removed rows: %d became %d", before, after)
	}
}

// The explanation is only worth reading if it matches what actually happens.
func TestTheDryRunAgreesWithTheRemoval(t *testing.T) {
	s := pruneFixture(t)

	explained, err := s.ExplainOutsideClosure([]string{"octo-org/app"})
	if err != nil {
		t.Fatalf("ExplainOutsideClosure: %v", err)
	}
	done, err := s.MarkOutsideClosure([]string{"octo-org/app"})
	if err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}

	if explained.Nodes != done.Nodes || explained.Edges != done.Edges {
		t.Errorf("explained %+v but removed %+v", explained, done)
	}
}

// The ledger is evidence that work happened. Deleting it to tidy the graph would record that a
// run did not occur, and a re-declared account would be analyzed from scratch rather than
// recognized as already on record.
func TestPruningLeavesTheLedgerAlone(t *testing.T) {
	s := pruneFixture(t)
	runs := countPrune(t, s, `SELECT COUNT(*) FROM abom_query_run`)
	findings := countPrune(t, s, `SELECT COUNT(*) FROM abom_finding`)

	if _, err := s.MarkOutsideClosure([]string{"octo-org/app"}); err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}

	if got := countPrune(t, s, `SELECT COUNT(*) FROM abom_query_run`); got != runs {
		t.Errorf("query runs %d became %d; the ledger is not collateral", runs, got)
	}
	if got := countPrune(t, s, `SELECT COUNT(*) FROM abom_finding`); got != findings {
		t.Errorf("findings %d became %d", findings, got)
	}
}

// Settling to zero is what makes this safe to run on a schedule.
func TestPruningTwiceRemovesNothingTheSecondTime(t *testing.T) {
	s := pruneFixture(t)
	if _, err := s.MarkOutsideClosure([]string{"octo-org/app"}); err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}

	again, err := s.MarkOutsideClosure([]string{"octo-org/app"})
	if err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}
	if again.Nodes != 0 || again.Edges != 0 {
		t.Errorf("the second pass removed %+v, want nothing", again)
	}
}

// An empty closure is what an undeclared corpus produces, and pruning everything on the strength
// of a configuration nobody has written yet is the most destructive thing this could do.
func TestAnEmptyClosureRefusesToPruneEverything(t *testing.T) {
	s := pruneFixture(t)

	if _, err := s.MarkOutsideClosure(nil); err == nil {
		t.Error("pruning against an empty closure must be refused, not obeyed")
	}
	if len(liveNodes(t, s)) == 0 {
		t.Error("the refused prune removed rows anyway")
	}
}

// Row ids stay monotonic so a client's delta cursor keeps meaning what it meant.
func TestMarkingDoesNotDeleteRows(t *testing.T) {
	s := pruneFixture(t)
	before := countPrune(t, s, `SELECT COUNT(*) FROM abom_node`)

	if _, err := s.MarkOutsideClosure([]string{"octo-org/app"}); err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}

	if after := countPrune(t, s, `SELECT COUNT(*) FROM abom_node`); after != before {
		t.Errorf("rows went %d -> %d; a tombstone is not a delete", before, after)
	}
}

func liveNodes(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT node_key FROM abom_node WHERE ` + LiveNode)
	if err != nil {
		t.Fatalf("reading live nodes: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading live nodes: %v", err)
	}
	return out
}

func contains(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

func countPrune(t *testing.T, s *Store, query string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// A removal is read against what is in view: "4 nodes" says nothing until it is 4 of 7. Counted
// from the live views, so what an earlier prune already took out is not counted twice.
func TestTheLiveGraphIsCountedWithoutWhatIsAlreadyRemoved(t *testing.T) {
	s := pruneFixture(t)
	before, err := s.LiveGraph()
	if err != nil {
		t.Fatalf("LiveGraph: %v", err)
	}
	if before.Nodes != len(liveNodes(t, s)) || before.Edges == 0 {
		t.Fatalf("live graph %+v, want every live node and the fixture's edges", before)
	}

	report, err := s.MarkOutsideClosure([]string{"octo-org/app"})
	if err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}
	after, err := s.LiveGraph()
	if err != nil {
		t.Fatalf("LiveGraph: %v", err)
	}
	if after.Nodes != before.Nodes-report.Nodes || after.Edges != before.Edges-report.Edges {
		t.Errorf("live graph %+v after removing %+v from %+v", after, report, before)
	}
}
