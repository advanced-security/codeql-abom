package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// workflowRunDDL holds each completed run attempt a workflow_run delivery reported: what check 5 samples.
const workflowRunDDL = `
CREATE TABLE IF NOT EXISTS abom_workflow_run (
  repository           TEXT NOT NULL,
  run_id               INTEGER NOT NULL,
  run_attempt          INTEGER NOT NULL DEFAULT 1,
  workflow_path        TEXT NOT NULL DEFAULT '',
  head_branch          TEXT NOT NULL DEFAULT '',
  head_sha             TEXT NOT NULL DEFAULT '',
  event                TEXT NOT NULL DEFAULT '',
  conclusion           TEXT NOT NULL DEFAULT '',
  -- NULL where the delivery did not say when the run completed.
  completed_at         TEXT,
  -- A JSON array of {path, sha, ref}, exactly as GitHub reported them.
  referenced_workflows TEXT NOT NULL DEFAULT '[]',
  recorded_at          TEXT NOT NULL,
  UNIQUE (repository, run_id, run_attempt)
);

CREATE INDEX IF NOT EXISTS idx_workflow_run_recent ON abom_workflow_run(completed_at, recorded_at);
`

// ReferencedWorkflow is a reusable workflow a run called, as its workflow_run delivery named it.
type ReferencedWorkflow struct {
	Path string `json:"path"`
	SHA  string `json:"sha"`
	Ref  string `json:"ref"`
}

// WorkflowRun is one completed attempt of a workflow run, as its workflow_run delivery reported it.
type WorkflowRun struct {
	Repository   string               `json:"repository"`
	RunID        int64                `json:"runId"`
	Attempt      int                  `json:"attempt"`
	WorkflowPath string               `json:"workflowPath"`
	HeadBranch   string               `json:"headBranch"`
	HeadSHA      string               `json:"headSha"`
	Event        string               `json:"event"`
	Conclusion   string               `json:"conclusion"`
	CompletedAt  time.Time            `json:"completedAt,omitzero"`
	Referenced   []ReferencedWorkflow `json:"referenced"`
}

// WorkflowRunFrame counts the runs kept and their repositories; Newest is nil where no kept run said when it completed.
type WorkflowRunFrame struct {
	Runs         int        `json:"runs"`
	Repositories int        `json:"repositories"`
	Newest       *time.Time `json:"newest,omitempty"`
}

// runTime is to the second, as GitHub reports times, and fixed width, so text order is time order.
func runTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// RecordWorkflowRun records one completed run attempt and reports whether it was new; a redelivery changes nothing.
func (s *Store) RecordWorkflowRun(r WorkflowRun) (bool, error) {
	repository := strings.ToLower(r.Repository)
	if repository == "" || r.RunID < 1 {
		return false, fmt.Errorf("store: a workflow run needs a repository and a run id; got %q and %d",
			r.Repository, r.RunID)
	}
	if r.Attempt < 0 {
		return false, fmt.Errorf("store: run %d of %s names attempt %d; attempts count from 1",
			r.RunID, repository, r.Attempt)
	}
	attempt := r.Attempt
	if attempt == 0 {
		// A payload naming no attempt is the first, which is what the column's default says.
		attempt = 1
	}
	referenced := r.Referenced
	if referenced == nil {
		referenced = []ReferencedWorkflow{}
	}
	doc, err := json.Marshal(referenced)
	if err != nil {
		return false, fmt.Errorf("recording run %d of %s: %w", r.RunID, repository, err)
	}
	var completed any
	if !r.CompletedAt.IsZero() {
		completed = runTime(r.CompletedAt)
	}
	res, err := s.db.Exec(`
		INSERT INTO abom_workflow_run (repository, run_id, run_attempt, workflow_path, head_branch,
			head_sha, event, conclusion, completed_at, referenced_workflows, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repository, run_id, run_attempt) DO NOTHING`,
		repository, r.RunID, attempt, r.WorkflowPath, r.HeadBranch, r.HeadSHA, r.Event, r.Conclusion,
		completed, string(doc), runTime(time.Now()))
	if err != nil {
		return false, fmt.Errorf("recording run %d of %s: %w", r.RunID, repository, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("recording run %d of %s: %w", r.RunID, repository, err)
	}
	return n == 1, nil
}

// workflowRunFrameQuery asks apart what one aggregate would answer by scanning the table and sorting.
const workflowRunFrameQuery = `SELECT
	(SELECT COUNT(*) FROM abom_workflow_run),
	(SELECT COUNT(*) FROM (SELECT DISTINCT repository FROM abom_workflow_run)),
	(SELECT MAX(completed_at) FROM abom_workflow_run)`

// WorkflowRunFrame counts the runs kept, the repositories they ran in, and the newest completion among them.
func (s *Store) WorkflowRunFrame() (WorkflowRunFrame, error) {
	var f WorkflowRunFrame
	var newest sql.NullString
	if err := s.db.QueryRow(workflowRunFrameQuery).Scan(&f.Runs, &f.Repositories, &newest); err != nil {
		return WorkflowRunFrame{}, fmt.Errorf("counting recorded workflow runs: %w", err)
	}
	if newest.Valid {
		t, err := time.Parse(time.RFC3339, newest.String)
		if err != nil {
			return WorkflowRunFrame{}, fmt.Errorf("reading the newest run's completion: %w", err)
		}
		f.Newest = &t
	}
	return f, nil
}

const recentWorkflowRunsQuery = `
	SELECT repository, run_id, run_attempt, workflow_path, head_branch, head_sha, event, conclusion,
	       completed_at, referenced_workflows
	  FROM abom_workflow_run
	 ORDER BY completed_at DESC, recorded_at DESC, rowid DESC
	 LIMIT ?`

// RecentWorkflowRuns returns up to limit runs, newest completed first; one that never said when follows them all.
func (s *Store) RecentWorkflowRuns(limit int) ([]WorkflowRun, error) {
	return queryAll(s, "reading recent workflow runs", scanWorkflowRun, recentWorkflowRunsQuery, limit)
}

func scanWorkflowRun(rows *sql.Rows) (WorkflowRun, error) {
	var r WorkflowRun
	var completed sql.NullString
	var referenced string
	if err := rows.Scan(&r.Repository, &r.RunID, &r.Attempt, &r.WorkflowPath, &r.HeadBranch, &r.HeadSHA,
		&r.Event, &r.Conclusion, &completed, &referenced); err != nil {
		return WorkflowRun{}, err
	}
	if completed.Valid {
		t, err := time.Parse(time.RFC3339, completed.String)
		if err != nil {
			return WorkflowRun{}, fmt.Errorf("run %d of %s: %w", r.RunID, r.Repository, err)
		}
		r.CompletedAt = t
	}
	if err := json.Unmarshal([]byte(referenced), &r.Referenced); err != nil {
		return WorkflowRun{}, fmt.Errorf("run %d of %s: %w", r.RunID, r.Repository, err)
	}
	if r.Referenced == nil {
		r.Referenced = []ReferencedWorkflow{}
	}
	return r, nil
}
