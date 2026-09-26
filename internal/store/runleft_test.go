package store

import (
	"testing"
	"time"
)

// leftRun records a run over app and tool, which have been analyzed, and never, which nothing
// tracks, so one member can never be done.
func leftRun(t *testing.T) (*Store, int64) {
	t.Helper()
	s := databaseScope(t)
	set, err := s.DatabasesMatching(FindingSelector{
		Repositories: []string{"octo-org/app", "octo-org/tool", "octo-org/never"},
	})
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	id, err := s.CreateAnalysisRun(AnalysisRequest{
		RequestedBy: "octocat",
		Queries:     []AnalysisQuery{{QueryID: "abom/edges", QueryVersion: "v1"}},
		Predicate:   "repositories=octo-org/app,octo-org/tool,octo-org/never",
		Mode:        set.Mode, Databases: set.Databases, Gaps: set.Gaps,
	})
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	return s, id
}

// at stamps a run's start, or a member's, where the test needs the clock to say something exact.
func at(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func stamp(tm time.Time) string { return tm.UTC().Format(time.RFC3339) }

func toDoOf(run AnalysisRun) map[string]bool {
	out := map[string]bool{}
	for _, m := range run.Members {
		out[m.Repository] = m.ToDo
	}
	return out
}

// Before Run, what is left is every member that can be done, and there is no time: nothing of
// this run has finished to take a pace from, and a forecast from elsewhere is not this run's.
func TestARunNotYetStartedCountsWhatIsLeftAndStatesNoTime(t *testing.T) {
	s, id := leftRun(t)

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if run.Left == nil {
		t.Fatal("a pending run says nothing about what is left")
	}
	if run.Left.Members != 2 {
		t.Errorf("left %d, want 2: octo-org/never can never be done", run.Left.Members)
	}
	if run.Left.RemainingSeconds != nil {
		t.Errorf("a run nothing of has finished states %ds remaining", *run.Left.RemainingSeconds)
	}
	want := map[string]bool{"octo-org/app": true, "octo-org/tool": true, "octo-org/never": false}
	for repo, todo := range want {
		if toDoOf(run)[repo] != todo {
			t.Errorf("%s still to do = %v, want %v", repo, toDoOf(run)[repo], todo)
		}
	}
}

// The time remaining is the pace of what this start finished, times what is left.
func TestATimeRemainingIsTakenFromWhatThisStartFinished(t *testing.T) {
	s, id := leftRun(t)
	if err := s.SetAnalysisRunState(id, RunRunning); err != nil {
		t.Fatalf("SetAnalysisRunState: %v", err)
	}
	start := time.Now().Add(-time.Hour)
	at(t, s, `UPDATE abom_analysis_run SET started_at = ? WHERE row_id = ?`, stamp(start), id)
	at(t, s, `UPDATE abom_analysis_member SET status = 'succeeded', started_at = ?, finished_at = ?
		WHERE run_id = ? AND repository = 'octo-org/app'`,
		stamp(start), stamp(start.Add(90*time.Second)), id)

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if run.Left == nil || run.Left.Members != 1 || run.Left.Finished != 1 {
		t.Fatalf("want 1 left and 1 finished, got %+v", run.Left)
	}
	if run.Left.RemainingSeconds == nil || *run.Left.RemainingSeconds != 90 {
		t.Errorf("remaining %v, want 90s: one member took 90s and one is left",
			run.Left.RemainingSeconds)
	}
	if toDoOf(run)["octo-org/app"] {
		t.Error("a member this start finished is still counted as to do")
	}
}

// Going again leaves each member's state from the last start until it is reached, so a member that
// finished before this start is still to do, and its duration is not this start's pace.
func TestAMemberFinishedInAnEarlierStartIsStillToDoAndSetsNoPace(t *testing.T) {
	s, id := leftRun(t)
	if err := s.SetAnalysisRunState(id, RunRunning); err != nil {
		t.Fatalf("SetAnalysisRunState: %v", err)
	}
	start := time.Now().Add(-time.Hour)
	at(t, s, `UPDATE abom_analysis_run SET started_at = ? WHERE row_id = ?`, stamp(start), id)
	at(t, s, `UPDATE abom_analysis_member SET status = 'succeeded', started_at = ?, finished_at = ?
		WHERE run_id = ? AND repository = 'octo-org/app'`,
		stamp(start.Add(-10*time.Minute)), stamp(start.Add(-time.Minute)), id)
	// Skipped this start without running: its start is the earlier one's, kept where it was.
	at(t, s, `UPDATE abom_analysis_member SET status = 'skipped', started_at = ?, finished_at = ?
		WHERE run_id = ? AND repository = 'octo-org/tool'`,
		stamp(start.Add(-10*time.Minute)), stamp(start.Add(time.Minute)), id)

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if !toDoOf(run)["octo-org/app"] {
		t.Error("a member finished only in an earlier start is not counted as still to do")
	}
	if toDoOf(run)["octo-org/tool"] {
		t.Error("a member this start reached is still counted as to do")
	}
	if run.Left == nil || run.Left.Members != 1 {
		t.Fatalf("want 1 left, got %+v", run.Left)
	}
	if run.Left.Finished != 0 || run.Left.RemainingSeconds != nil {
		t.Errorf("a pace was taken from members this start did not run: %+v", run.Left)
	}
}

// Every start stamps its own beginning, which is what "this start" is measured from.
func TestEachStartOfARunStampsWhenItBegan(t *testing.T) {
	s, id := leftRun(t)
	if err := s.SetAnalysisRunState(id, RunRunning); err != nil {
		t.Fatalf("SetAnalysisRunState: %v", err)
	}
	old := stamp(time.Now().Add(-time.Hour))
	at(t, s, `UPDATE abom_analysis_run SET started_at = ? WHERE row_id = ?`, old, id)
	if err := s.SetAnalysisRunState(id, RunComplete); err != nil {
		t.Fatalf("SetAnalysisRunState: %v", err)
	}
	done, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if done.StartedAt != old {
		t.Errorf("finishing moved the start from %s to %s", old, done.StartedAt)
	}
	if done.Left != nil {
		t.Errorf("a complete run says %+v is left", done.Left)
	}

	if err := s.SetAnalysisRunState(id, RunRunning); err != nil {
		t.Fatalf("SetAnalysisRunState: %v", err)
	}
	again, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if again.StartedAt == "" || again.StartedAt == old {
		t.Errorf("starting again left the start at %q", again.StartedAt)
	}
}

// A member building its database and then analyzing it is one member at work: moving it to running
// again must not restart its clock, or the pace leaves out every build and runs short.
func TestAMemberAlreadyRunningKeepsWhenItStarted(t *testing.T) {
	s, id := leftRun(t)
	app := DatabaseIdentity{
		Repository: "octo-org/app", Commit: "sha-app", SourceFilter: "f0", Mode: AnalysisModeRegular,
	}
	if err := s.SetAnalysisMemberStatus(id, app, AnalysisRunning, "building its database"); err != nil {
		t.Fatalf("SetAnalysisMemberStatus: %v", err)
	}
	began := stamp(time.Now().Add(-time.Minute))
	at(t, s, `UPDATE abom_analysis_member SET started_at = ? WHERE run_id = ? AND repository = ?`,
		began, id, app.Repository)
	if err := s.SetAnalysisMemberStatus(id, app, AnalysisRunning, ""); err != nil {
		t.Fatalf("SetAnalysisMemberStatus: %v", err)
	}

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	for _, m := range run.Members {
		if m.Repository == app.Repository && m.StartedAt != began {
			t.Errorf("analyzing after building restarted the member at %s, want %s", m.StartedAt, began)
		}
	}

	// Finished, then reached by a later start: that is a start, and its clock begins again.
	if err := s.SetAnalysisMemberStatus(id, app, AnalysisSucceeded, ""); err != nil {
		t.Fatalf("SetAnalysisMemberStatus: %v", err)
	}
	if err := s.SetAnalysisMemberStatus(id, app, AnalysisRunning, ""); err != nil {
		t.Fatalf("SetAnalysisMemberStatus: %v", err)
	}
	again, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	for _, m := range again.Members {
		if m.Repository == app.Repository && m.StartedAt == began {
			t.Error("a member reached again by a later start kept the earlier start's clock")
		}
	}
}

// The list of runs says what is left without carrying the members it refuses to carry.
func TestTheListOfRunsSaysWhatIsLeftAsTheRunDoes(t *testing.T) {
	s, id := leftRun(t)
	one, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	list, err := s.AnalysisRuns()
	if err != nil {
		t.Fatalf("AnalysisRuns: %v", err)
	}
	if len(list) != 1 || list[0].Left == nil || *list[0].Left != *one.Left {
		t.Errorf("the list says %+v is left and the run says %+v", list[0].Left, one.Left)
	}
}
