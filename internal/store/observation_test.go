package store

import "testing"

func testRun(t *testing.T, s *Store, queryID string) int64 {
	t.Helper()
	id, err := s.RecordQueryRun(QueryRun{
		QueryID: queryID, QueryVersion: "1", PackVersion: "0.1.0",
		Repository: "octo/app", DBCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	return id
}

// The point of this table is that a query nobody has written yet can contribute facts without a
// schema change or a line of Go. What it may never do is contribute an unattributed fact: the
// whole model rests on every claim naming the query that made it, exactly as a finding names its
// rule id.
func TestAnObservationCarriesTheQueryThatProducedIt(t *testing.T) {
	s := newTestStore(t)
	run := testRun(t, s, "abom/step-reachability")

	n, err := s.AddObservations(run, []Observation{{
		SubjectKind: SubjectNode, SubjectKey: "actions/checkout",
		Name: "reachable", Value: "true",
	}})
	if err != nil {
		t.Fatalf("AddObservations: %v", err)
	}
	if n != 1 {
		t.Fatalf("wrote %d observations, want 1", n)
	}

	got, err := s.ObservationsOf(SubjectNode, "actions/checkout")
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read back %d observations, want 1", len(got))
	}
	if got[0].QueryID != "abom/step-reachability" {
		t.Errorf("QueryID = %q, want the query that produced it", got[0].QueryID)
	}
	if got[0].Name != "reachable" || got[0].Value != "true" {
		t.Errorf("got %+v, want the recorded name and value", got[0])
	}
}

// Re-running a query must refresh rather than accumulate, or a database analyzed twice would
// report every fact twice and any count drawn from it would drift upward on its own.
func TestReRunningAQueryRefreshesItsObservations(t *testing.T) {
	s := newTestStore(t)
	run := testRun(t, s, "abom/step-reachability")
	obs := Observation{
		SubjectKind: SubjectNode, SubjectKey: "actions/checkout",
		Name: "reachable", Value: "true",
	}
	if _, err := s.AddObservations(run, []Observation{obs}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	obs.Value = "false"
	if _, err := s.AddObservations(run, []Observation{obs}); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := s.ObservationsOf(SubjectNode, "actions/checkout")
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read back %d observations, want the one refreshed row", len(got))
	}
	if got[0].Value != "false" {
		t.Errorf("Value = %q, want the re-run's answer", got[0].Value)
	}
}

// Two queries may say different things about the same subject, and both are kept: they are
// separate statements by separate authors, and collapsing them would silently pick a winner.
func TestTwoQueriesMayObserveTheSameSubject(t *testing.T) {
	s := newTestStore(t)
	for _, q := range []string{"abom/one", "abom/two"} {
		run := testRun(t, s, q)
		if _, err := s.AddObservations(run, []Observation{{
			SubjectKind: SubjectNode, SubjectKey: "actions/checkout",
			Name: "verdict", Value: q,
		}}); err != nil {
			t.Fatalf("AddObservations(%s): %v", q, err)
		}
	}

	got, err := s.ObservationsOf(SubjectNode, "actions/checkout")
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("read back %d observations, want one per query", len(got))
	}
}

// An observation naming no subject cannot be attached to anything, so it would be a fact about
// nothing sitting in a table that exists to attach facts to things.
func TestAnObservationWithoutASubjectIsRejected(t *testing.T) {
	s := newTestStore(t)
	run := testRun(t, s, "abom/one")
	if _, err := s.AddObservations(run, []Observation{{
		SubjectKind: SubjectNode, Name: "reachable", Value: "true",
	}}); err == nil {
		t.Error("an observation with no subject key was accepted")
	}
}

// CodeQL is deterministic: the same query at the same version against the same database gives
// the same answer. Recognizing that is what makes re-processing cheap enough to offer, because
// only what actually changed has to run again.
func TestARunAlreadyRecordedIsRecognized(t *testing.T) {
	s := newTestStore(t)
	run := QueryRun{
		QueryID: "abom/edges", QueryVersion: "1", PackVersion: "0.1.0",
		Repository: "octo/app", DBCommit: "abc123",
	}

	done, err := s.QueryRunExists(run)
	if err != nil {
		t.Fatalf("QueryRunExists: %v", err)
	}
	if done {
		t.Fatal("a run that never happened was reported as done")
	}

	if _, err := s.RecordQueryRun(run); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if done, err = s.QueryRunExists(run); err != nil || !done {
		t.Errorf("QueryRunExists = %v (%v), want true once recorded", done, err)
	}

	// A different commit is different work, however identical the query.
	moved := run
	moved.DBCommit = "def456"
	if done, err = s.QueryRunExists(moved); err != nil || done {
		t.Errorf("QueryRunExists on a new commit = %v (%v), want false", done, err)
	}

	// So is an amended query against the same commit.
	amended := run
	amended.QueryVersion = "2"
	if done, err = s.QueryRunExists(amended); err != nil || done {
		t.Errorf("QueryRunExists on an amended query = %v (%v), want false", done, err)
	}
}
