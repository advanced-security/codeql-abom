package store

import "testing"

// A delta could only ever add, so a client was never told something disappeared — it would hold a
// pruned node for as long as the page stayed open.
func TestADeltaReportsWhatWasRemoved(t *testing.T) {
	s := pruneFixture(t)
	before := generationOf(t, s)

	if _, err := s.MarkOutsideClosure([]string{"octo-org/app"}); err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}

	delta, err := s.GraphDelta(before, FullGraphOptions{})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if len(delta.RemovedNodes) == 0 {
		t.Fatal("nodes were removed and the delta says nothing")
	}
	if !contains(delta.RemovedNodes, "stranger/thing") {
		t.Errorf("RemovedNodes = %v, want the pruned node", delta.RemovedNodes)
	}
	if contains(delta.RemovedNodes, "actions/checkout") {
		t.Error("a node inside the closure was reported as removed")
	}
}

// A client that already knows about a removal must not be handed it again, or every poll after a
// prune repeats work that is already done.
func TestARemovalIsReportedOnceToACurrentClient(t *testing.T) {
	s := pruneFixture(t)
	if _, err := s.MarkOutsideClosure([]string{"octo-org/app"}); err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}
	current := generationOf(t, s)

	delta, err := s.GraphDelta(current, FullGraphOptions{})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if len(delta.RemovedNodes) != 0 {
		t.Errorf("a client already past the prune was told again: %v", delta.RemovedNodes)
	}
}

// Compaction is what stops a tombstone becoming the ever-growing graph under another name.
func TestCompactionReclaimsTombstonedRows(t *testing.T) {
	s := pruneFixture(t)
	if _, err := s.MarkOutsideClosure([]string{"octo-org/app"}); err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}
	before := countPrune(t, s, `SELECT COUNT(*) FROM abom_node`)

	report, err := s.Compact()
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if report.Nodes == 0 {
		t.Fatal("compaction reclaimed nothing")
	}
	if after := countPrune(t, s, `SELECT COUNT(*) FROM abom_node`); after >= before {
		t.Errorf("rows went %d -> %d; compaction deletes", before, after)
	}
	if n := countPrune(t, s, `SELECT COUNT(*) FROM abom_node_live`); n == 0 {
		t.Error("compaction removed what was still live")
	}
}

// Row ids stop meaning what a client's cursor thinks they mean, so every client has to reload
// rather than be handed a delta that skips work.
func TestCompactionForcesEveryClientToReload(t *testing.T) {
	s := pruneFixture(t)
	if _, err := s.MarkOutsideClosure([]string{"octo-org/app"}); err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}
	cursor := generationOf(t, s)

	if _, err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	delta, err := s.GraphDelta(cursor, FullGraphOptions{})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if !delta.Reset {
		t.Error("a cursor from before the compaction must be refused, not honored")
	}
}

// A refused cursor is correct; a cursor that can never be honored is not.
//
// The compaction bumps the epoch, and every generation recorded afterwards has to carry it. When
// it does not, the epoch comparison can never match again: the delta answers reset for the rest of
// the database's life and every open page silently reloads the whole payload on every poll.
// Nothing logs it and the interface looks correct throughout.
//
// This test compacts first on purpose. A delta test that does not passes on a corpus where every
// epoch is already equal — which is every corpus that has never been compacted, and is why this
// survived at least one compaction unnoticed.
func TestAGenerationRecordedAfterACompactionIsStillHonored(t *testing.T) {
	s := pruneFixture(t)
	if _, err := s.MarkOutsideClosure([]string{"octo-org/app"}); err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}
	stale := generationOf(t, s)
	if _, err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	fresh, err := s.RecordGraphGeneration(GraphRebuild{})
	if err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}

	delta, err := s.GraphDelta(fresh, FullGraphOptions{})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if delta.Reset {
		t.Error("a generation recorded after the compaction was refused, so no cursor can ever " +
			"be honored again and every client reloads forever")
	}

	// The guard still has to mean what it meant: the point is to record the epoch honestly, not
	// to stop checking it.
	if stale != fresh {
		before, err := s.GraphDelta(stale, FullGraphOptions{})
		if err != nil {
			t.Fatalf("GraphDelta(stale): %v", err)
		}
		if !before.Reset {
			t.Error("a cursor from before the compaction was honored; its row ids name rows " +
				"that no longer exist")
		}
	}
}

// The foreign key closure is the part that bit when the junk `/` repository was deleted by hand:
// `abom_finding_node` and edges naming a node as an endpoint both held it.
func TestCompactionLeavesNoDanglingReferences(t *testing.T) {
	s := pruneFixture(t)
	if _, err := s.MarkOutsideClosure([]string{"octo-org/app"}); err != nil {
		t.Fatalf("MarkOutsideClosure: %v", err)
	}
	if _, err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	rows, err := s.DB().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, parent string
		var rowid, fkid any
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		t.Errorf("%s holds a reference to a compacted %s", table, parent)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
}

// Compacting a corpus nothing was pruned from must be a no-op rather than an epoch bump that
// makes every open page reload for nothing.
func TestCompactingWithNothingTombstonedChangesNothing(t *testing.T) {
	s := pruneFixture(t)
	cursor := generationOf(t, s)

	report, err := s.Compact()
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if report.Nodes != 0 || report.Edges != 0 {
		t.Errorf("compaction removed %+v from a corpus with no tombstones", report)
	}

	delta, err := s.GraphDelta(cursor, FullGraphOptions{})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if delta.Reset {
		t.Error("a no-op compaction forced every client to reload")
	}
}

func generationOf(t *testing.T, s *Store) int64 {
	t.Helper()
	v, err := s.GraphVersion()
	if err != nil {
		t.Fatalf("GraphVersion: %v", err)
	}
	return v.Generation
}
