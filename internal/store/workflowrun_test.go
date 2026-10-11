package store

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func recordCompletedRun(t *testing.T, s *Store, r WorkflowRun) bool {
	t.Helper()
	fresh, err := s.RecordWorkflowRun(r)
	if err != nil {
		t.Fatalf("RecordWorkflowRun(%s #%d): %v", r.Repository, r.RunID, err)
	}
	return fresh
}

func recentRunsOf(t *testing.T, s *Store, limit int) []WorkflowRun {
	t.Helper()
	runs, err := s.RecentWorkflowRuns(limit)
	if err != nil {
		t.Fatalf("RecentWorkflowRuns(%d): %v", limit, err)
	}
	return runs
}

func runFrameOf(t *testing.T, s *Store) WorkflowRunFrame {
	t.Helper()
	frame, err := s.WorkflowRunFrame()
	if err != nil {
		t.Fatalf("WorkflowRunFrame: %v", err)
	}
	return frame
}

func setRecordedAt(t *testing.T, s *Store, runID int64, at time.Time) {
	t.Helper()
	if _, err := s.DB().Exec(`UPDATE abom_workflow_run SET recorded_at = ? WHERE run_id = ?`,
		at.UTC().Format(time.RFC3339), runID); err != nil {
		t.Fatalf("dating run %d: %v", runID, err)
	}
}

func runIDs(runs []WorkflowRun) []int64 {
	ids := []int64{}
	for _, r := range runs {
		ids = append(ids, r.RunID)
	}
	return ids
}

// GitHub redelivers on request; a run recorded twice would be sampled twice and counted twice.
func TestARedeliveredWorkflowRunIsRecordedOnce(t *testing.T) {
	s := newTestStore(t)
	first := WorkflowRun{Repository: "Acme/Service", RunID: 42, Attempt: 1, Conclusion: "success"}

	if !recordCompletedRun(t, s, first) {
		t.Fatal("the first delivery of a run was reported as a repeat")
	}
	again := first
	again.Repository, again.Conclusion = "acme/service", "failure"
	if recordCompletedRun(t, s, again) {
		t.Error("a redelivery of the same attempt, named in another case, was recorded as new")
	}
	rerun := first
	rerun.Attempt = 2
	if !recordCompletedRun(t, s, rerun) {
		t.Error("a re-run is an attempt of its own and was reported as a repeat")
	}

	runs := recentRunsOf(t, s, 10)
	if len(runs) != 2 {
		t.Fatalf("recorded %d runs, want the two attempts: %+v", len(runs), runs)
	}
	for _, r := range runs {
		if r.Repository != "acme/service" {
			t.Errorf("repository = %q, want it lowercased so both spellings name one repository", r.Repository)
		}
		if r.Attempt == 1 && r.Conclusion != "success" {
			t.Errorf("attempt 1 reads %q, want the first delivery kept", r.Conclusion)
		}
	}
	if frame := runFrameOf(t, s); frame.Runs != 2 || frame.Repositories != 1 {
		t.Errorf("frame = %+v, want 2 runs in 1 repository", frame)
	}
}

// A run nobody can name cannot be sampled or matched to a commit, so it is refused rather than kept.
func TestAWorkflowRunWithoutARepositoryOrRunIDIsRefused(t *testing.T) {
	s := newTestStore(t)
	for name, r := range map[string]WorkflowRun{
		"no repository":      {RunID: 1},
		"no run id":          {Repository: "acme/service"},
		"a negative run id":  {Repository: "acme/service", RunID: -1},
		"a negative attempt": {Repository: "acme/service", RunID: 1, Attempt: -1},
	} {
		if _, err := s.RecordWorkflowRun(r); err == nil {
			t.Errorf("%s: recorded, want refused", name)
		}
	}
	if frame := runFrameOf(t, s); frame.Runs != 0 {
		t.Errorf("frame = %+v after every refusal, want nothing recorded", frame)
	}
}

// Check 5 compares these with what the graph says the run called, so they must come back as GitHub said.
func TestReferencedWorkflowsComeBackExactlyAsGitHubReportedThem(t *testing.T) {
	s := newTestStore(t)
	run := WorkflowRun{
		Repository: "acme/service", RunID: 7, Attempt: 3, WorkflowPath: ".github/workflows/ci.yml",
		HeadBranch: "main", HeadSHA: "f00dfacef00dfacef00dfacef00dfacef00dface", Event: "push",
		Conclusion: "success", CompletedAt: time.Date(2026, 10, 1, 12, 30, 5, 0, time.UTC),
		Referenced: []ReferencedWorkflow{
			{Path: "acme/shared/.github/workflows/build.yml@main",
				SHA: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", Ref: "refs/heads/main"},
			{Path: "acme/shared/.github/workflows/deploy.yml@0123456789abcdef0123456789abcdef01234567",
				SHA: "0123456789abcdef0123456789abcdef01234567"},
		},
	}
	recordCompletedRun(t, s, run)
	recordCompletedRun(t, s, WorkflowRun{Repository: "acme/service", RunID: 8, CompletedAt: run.CompletedAt.Add(-time.Hour)})

	runs := recentRunsOf(t, s, 10)
	if len(runs) != 2 {
		t.Fatalf("read %d runs, want 2", len(runs))
	}
	got := runs[0]
	if !got.CompletedAt.Equal(run.CompletedAt) {
		t.Errorf("completedAt = %v, want %v", got.CompletedAt, run.CompletedAt)
	}
	got.CompletedAt, run.CompletedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, run) {
		t.Errorf("read back\n %+v\nwant\n %+v", got, run)
	}
	if runs[1].Referenced == nil || len(runs[1].Referenced) != 0 {
		t.Errorf("a run that called no reusable workflow reads %#v, want an empty list", runs[1].Referenced)
	}
	body, _ := json.Marshal(runs[1])
	if !strings.Contains(string(body), `"referenced":[]`) {
		t.Errorf("a run calling nothing marshals as %s, want an empty list rather than null", body)
	}
}

// A run that did not say when it completed has no completion time, which is not the year 1.
func TestAnAbsentCompletionTimeIsNotRecordedAsATime(t *testing.T) {
	s := newTestStore(t)
	recordCompletedRun(t, s, WorkflowRun{Repository: "acme/service", RunID: 9})

	var completed sql.NullString
	if err := s.DB().QueryRow(`SELECT completed_at FROM abom_workflow_run WHERE run_id = 9`).
		Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed.Valid {
		t.Errorf("completed_at = %q, want NULL", completed.String)
	}
	runs := recentRunsOf(t, s, 1)
	if len(runs) != 1 || !runs[0].CompletedAt.IsZero() {
		t.Fatalf("read %+v, want one run with no completion time", runs)
	}
	body, _ := json.Marshal(runs[0])
	if strings.Contains(string(body), "completedAt") {
		t.Errorf("a run with no completion time marshals as %s, want completedAt absent", body)
	}
	if frame := runFrameOf(t, s); frame.Runs != 1 || frame.Newest != nil {
		t.Errorf("frame = %+v, want one run and no newest completion, since none was reported", frame)
	}
}

// A payload that names no attempt is the first, so a later delivery naming attempt 1 is the same run.
func TestARunThatNamesNoAttemptIsItsFirstAttempt(t *testing.T) {
	s := newTestStore(t)
	if !recordCompletedRun(t, s, WorkflowRun{Repository: "acme/service", RunID: 5}) {
		t.Fatal("the first delivery of a run was reported as a repeat")
	}
	if recordCompletedRun(t, s, WorkflowRun{Repository: "acme/service", RunID: 5, Attempt: 1}) {
		t.Error("attempt 1 of a run already recorded without an attempt was recorded again")
	}
	if runs := recentRunsOf(t, s, 10); len(runs) != 1 || runs[0].Attempt != 1 {
		t.Errorf("read %+v, want one run, attempt 1", runs)
	}
}

// Check 5 is not built yet, so the frame is what shows the runs it will sample are arriving.
func TestTheRunFrameCountsWhatIsKeptAndTheNewestCompletion(t *testing.T) {
	s := newTestStore(t)

	fresh := runFrameOf(t, s)
	if fresh.Runs != 0 || fresh.Repositories != 0 || fresh.Newest != nil {
		t.Errorf("a fresh store's frame = %+v, want no runs and no newest", fresh)
	}
	if body, _ := json.Marshal(fresh); string(body) != `{"runs":0,"repositories":0}` {
		t.Errorf("a fresh frame marshals as %s, want no newest rather than a zero time", body)
	}

	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	recordCompletedRun(t, s, WorkflowRun{Repository: "acme/a", RunID: 1, CompletedAt: base})
	recordCompletedRun(t, s, WorkflowRun{Repository: "acme/a", RunID: 2, CompletedAt: base.Add(2 * time.Hour)})
	recordCompletedRun(t, s, WorkflowRun{Repository: "acme/b", RunID: 3, CompletedAt: base.Add(time.Hour)})
	recordCompletedRun(t, s, WorkflowRun{Repository: "acme/a", RunID: 1, CompletedAt: base.Add(5 * time.Hour)})

	frame := runFrameOf(t, s)
	if frame.Runs != 3 || frame.Repositories != 2 {
		t.Errorf("frame = %+v, want 3 runs in 2 repositories", frame)
	}
	if frame.Newest == nil || !frame.Newest.Equal(base.Add(2*time.Hour)) {
		t.Errorf("newest = %v, want %v: the latest completion kept, not a redelivery's", frame.Newest,
			base.Add(2*time.Hour))
	}
}

// Samples take what changed most recently first; a run that never said when it completed goes last.
func TestRecentWorkflowRunsComeNewestCompletedFirst(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, r := range []WorkflowRun{
		{Repository: "acme/a", RunID: 1, CompletedAt: base.Add(time.Hour)},
		{Repository: "acme/a", RunID: 2, CompletedAt: base.Add(2 * time.Hour)},
		{Repository: "acme/b", RunID: 3},
		{Repository: "acme/b", RunID: 4, CompletedAt: base},
		{Repository: "acme/b", RunID: 5},
		{Repository: "acme/c", RunID: 6, CompletedAt: base.Add(time.Hour)},
	} {
		recordCompletedRun(t, s, r)
	}
	// Recorded against the order they were inserted in, so the tie-break is the recording, not the row.
	setRecordedAt(t, s, 1, base.Add(3*time.Hour))
	setRecordedAt(t, s, 6, base.Add(2*time.Hour))
	setRecordedAt(t, s, 3, base.Add(5*time.Hour))
	setRecordedAt(t, s, 5, base.Add(4*time.Hour))

	want := []int64{2, 1, 6, 4, 3, 5}
	if got := runIDs(recentRunsOf(t, s, 10)); !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if got := runIDs(recentRunsOf(t, s, 2)); !reflect.DeepEqual(got, want[:2]) {
		t.Errorf("the newest two = %v, want %v", got, want[:2])
	}
}

// The interface calls .Select() on what this returns; nil marshals to null and throws there.
func TestNoRecordedRunsIsAnEmptyListNotNull(t *testing.T) {
	s := newTestStore(t)
	runs := recentRunsOf(t, s, 10)
	if runs == nil || len(runs) != 0 {
		t.Fatalf("RecentWorkflowRuns on nothing = %#v, want an empty list", runs)
	}
	if body, _ := json.Marshal(runs); string(body) != "[]" {
		t.Errorf("marshals as %s, want []", body)
	}
}

// Check 5 samples the newest runs and /api/checks reads the frame through the one connection every read
// waits on, so each walks an index rather than the table and a sort.
func TestSQLiteNewestRunsAndTheRunFrameAreReadFromIndexes(t *testing.T) {
	s := newTestStore(t)
	recent := explain(t, s, recentWorkflowRunsQuery, 10)
	if strings.Contains(recent, "TEMP B-TREE") || !strings.Contains(recent, "idx_workflow_run_recent") {
		t.Errorf("the newest runs are read with\n%s\nwant idx_workflow_run_recent and no sort", recent)
	}
	frame := explain(t, s, workflowRunFrameQuery)
	for _, step := range strings.Split(frame, "\n") {
		if strings.Contains(step, "TEMP B-TREE") ||
			strings.Contains(step, "abom_workflow_run") && !strings.Contains(step, "COVERING INDEX") {
			t.Errorf("the frame is read with\n%s\nwant every step on a covering index and no sort", frame)
			break
		}
	}
}
