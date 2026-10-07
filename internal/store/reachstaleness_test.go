package store

import "testing"

// A reach row written before this build counted a figure holds NULL for it, and only rebuilding
// fills it; a row this build wrote is complete.
func TestReachWrittenBeforeItsFiguresExistedIsIncomplete(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "actions/checkout", "v4", 10)
	write := func(steps any) {
		t.Helper()
		if _, err := s.DB().Exec(`DELETE FROM abom_blast_radius`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`INSERT INTO abom_blast_radius (node_row_id, dependent_repos,
			dependent_steps, dependent_jobs, dependent_workflows, max_depth, refs_in_use)
			SELECT row_id, 1, ?, 1, 1, 1, 1 FROM abom_node WHERE node_key = 'actions/checkout'`, steps); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		steps      any
		incomplete bool
	}{{nil, true}, {2, false}} {
		write(c.steps)
		st, err := s.ReachStaleness()
		if err != nil {
			t.Fatal(err)
		}
		if st.Incomplete != c.incomplete {
			t.Errorf("steps %v: incomplete = %v, want %v", c.steps, st.Incomplete, c.incomplete)
		}
	}
}

// Reset by incremental derivations, the rebuild would never run on a crawl that ingests every cycle.
func TestTheRebuildScheduleCountsFromTheLastRebuildAndPendingFromAnyDerivation(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "actions/checkout", "v4", 10)
	if _, err := s.RecordGraphGeneration(GraphRebuild{ReachRebuilt: true}); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := s.ReachStaleness()
	if err != nil {
		t.Fatal(err)
	}
	seedDependency(t, s, "octo-org/other", "actions/cache", "v4", 11)
	before, err := s.ReachStaleness()
	if err != nil {
		t.Fatal(err)
	}
	if before.Pending != 1 {
		t.Errorf("%d dependencies pending before any derivation saw them, want 1", before.Pending)
	}
	if _, err := s.RecordGraphGeneration(GraphRebuild{ReachRebuilt: true, Incremental: true}); err != nil {
		t.Fatal(err)
	}

	st, err := s.ReachStaleness()
	if err != nil {
		t.Fatal(err)
	}
	if st.Never || !st.At.Equal(rebuilt.At) {
		t.Errorf("the rebuild schedule reads %+v; it must still count from the rebuild at %v", st, rebuilt.At)
	}
	if st.NewEdges != 1 {
		t.Errorf("%d dependencies counted toward the next rebuild, want the 1 ingested since it", st.NewEdges)
	}
	if st.Pending != 0 {
		t.Errorf("%d dependencies pending, but the incremental derivation measured every one", st.Pending)
	}
}

// Incremental derivation starts from a rebuild, so without one reach was never established.
func TestAnIncrementalGenerationAloneIsNotAMeasurement(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "actions/checkout", "v4", 10)
	if _, err := s.RecordGraphGeneration(GraphRebuild{ReachRebuilt: true, Incremental: true}); err != nil {
		t.Fatal(err)
	}
	st, err := s.ReachStaleness()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Never {
		t.Errorf("staleness %+v; with no rebuild on record reach was never established", st)
	}
}

// A drained queue rebuilds only when something changed since the last rebuild, and every cycle that
// ingested or moved a derived row records a generation, so the generations since say whether it did.
func TestTheCyclesThatChangedTheGraphSinceTheLastRebuildAreCounted(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "actions/checkout", "v4", 10)
	count := func(want int, after string) {
		t.Helper()
		st, err := s.ReachStaleness()
		if err != nil {
			t.Fatal(err)
		}
		if st.Generations != want {
			t.Errorf("after %s, %d generations since the last rebuild, want %d", after, st.Generations, want)
		}
	}
	if _, err := s.RecordGraphGeneration(GraphRebuild{ReachRebuilt: true}); err != nil {
		t.Fatal(err)
	}
	count(0, "a rebuild")
	for range 2 {
		if _, err := s.RecordGraphGeneration(GraphRebuild{ReachRebuilt: true, Incremental: true}); err != nil {
			t.Fatal(err)
		}
	}
	count(2, "two incremental derivations")
	if _, err := s.RecordGraphGeneration(GraphRebuild{ReachRebuilt: true}); err != nil {
		t.Fatal(err)
	}
	count(0, "the next rebuild")
}
