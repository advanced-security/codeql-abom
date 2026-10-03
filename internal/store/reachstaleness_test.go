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
