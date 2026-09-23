package store

import "testing"

// Left as running, an abandoned run could be neither stopped nor started; canceled would be untrue.
func TestARunLeftRunningByAServerThatStoppedIsInterruptedNotCanceled(t *testing.T) {
	s := newTestStore(t)
	id := startedRun(t, s)
	app := DatabaseIdentity{
		Repository: "octo/app", Commit: "sha-one", SourceFilter: "f0", Mode: AnalysisModeRegular,
	}
	if err := s.SetAnalysisRunState(id, RunRunning); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAnalysisMemberStatus(id, app, AnalysisRunning, ""); err != nil {
		t.Fatal(err)
	}
	finished := startedRun(t, s)
	if err := s.SetAnalysisRunState(finished, RunComplete); err != nil {
		t.Fatal(err)
	}

	n, err := s.InterruptAbandonedRuns()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("interrupted %d runs, want the one left running", n)
	}

	run, _ := s.AnalysisRun(id)
	if run.State != RunInterrupted {
		t.Errorf("the abandoned run is %q, want %q", run.State, RunInterrupted)
	}
	for _, m := range run.Members {
		if m.Repository == "octo/app" && (m.Status != AnalysisPending || m.Detail == "") {
			t.Errorf("the member in flight is %q (%q), want pending with the reason", m.Status, m.Detail)
		}
	}
	if done, _ := s.AnalysisRun(finished); done.State != RunComplete {
		t.Errorf("a finished run became %q", done.State)
	}
}
