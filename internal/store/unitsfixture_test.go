package store

import "testing"

// workflowWithJobs builds the shape the extractor produces: a workflow node that declares a job and
// its steps, each step carrying the `job_id` that names its parent, with the container link
// pointing at the declaring file rather than at the job. Each step uses one dependency, so a
// job's dependency count is its own steps' and not its siblings'.
func workflowWithJobs(t *testing.T, s *Store, repo, path, jobID string, steps int) {
	t.Helper()
	repoID, err := s.UpsertRepository(repo, "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	wfKey := repo + "/" + path
	wfID := upsertNode(t, s, nodeRow{
		Key: wfKey, Kind: "workflow", Repo: repoID, Path: path, StepIndex: -1,
	})
	upsertNode(t, s, nodeRow{
		Key: "job:" + repo + ":" + path + ":" + jobID, Kind: "job", Repo: repoID, Path: path,
		JobID: jobID, StepIndex: -1, Container: wfID,
	})

	for i := range steps {
		stepID := upsertNode(t, s, nodeRow{
			Key:  "step:" + repo + ":" + path + ":" + jobID + ":" + itoa(i),
			Kind: "step", Repo: repoID, Path: path, JobID: jobID, StepIndex: i, Container: wfID,
		})
		dep := upsertNode(t, s, nodeRow{
			Key: "vendor/" + jobID + "-dep-" + itoa(i), Kind: "action", External: true,
		})
		if _, err := s.DB().Exec(`
			INSERT INTO abom_edge (edge_key, src_node_row_id, dst_node_row_id, edge_kind,
			                       ref_value, repository_row_id)
			VALUES (?, ?, ?, 'step-uses-action', 'v1', ?)
			ON CONFLICT(edge_key) DO NOTHING`,
			wfKey+jobID+itoa(i), stepID, dep, repoID); err != nil {
			t.Fatalf("recording a uses edge: %v", err)
		}
	}
}

type nodeRow struct {
	Key       string
	Kind      string
	Repo      int64
	Path      string
	JobID     string
	StepIndex int
	Container int64
	External  bool
}

func upsertNode(t *testing.T, s *Store, n nodeRow) int64 {
	t.Helper()
	var repo, container any
	if n.Repo != 0 {
		repo = n.Repo
	}
	if n.Container != 0 {
		container = n.Container
	}
	external := 0
	if n.External {
		external = 1
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_node (node_key, node_kind, repository_row_id, path, job_id, step_index,
		                       is_external, container_node_row_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_key) DO NOTHING`,
		n.Key, n.Kind, repo, nullable(n.Path), nullable(n.JobID), n.StepIndex, external,
		container); err != nil {
		t.Fatalf("adding a %s node: %v", n.Kind, err)
	}
	var id int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM abom_node WHERE node_key = ?`, n.Key).Scan(&id); err != nil {
		t.Fatalf("resolving %s: %v", n.Key, err)
	}
	return id
}

// observe records a fact a query stated about a node, as the graph extraction does. Observations
// are evidence, so every one belongs to the run that made it — the column is NOT NULL for that
// reason and a fixture that skipped it would be recording a fact nobody claimed.
func observe(t *testing.T, s *Store, subject, name, value string) {
	t.Helper()
	var runID int64
	if err := s.DB().QueryRow(`SELECT row_id FROM abom_query_run LIMIT 1`).Scan(&runID); err != nil {
		res, err := s.DB().Exec(`
			INSERT INTO abom_query_run (run_key, query_id, repository, ran_at)
			VALUES ('fixture-run', 'fixture/observations', 'octo-org/app', '2026-01-01T00:00:00Z')`)
		if err != nil {
			t.Fatalf("recording a query run: %v", err)
		}
		if runID, err = res.LastInsertId(); err != nil {
			t.Fatalf("LastInsertId: %v", err)
		}
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_observation (query_run_id, subject_kind, subject_key, name, value)
		VALUES (?, 'node', ?, ?, ?)
		ON CONFLICT(subject_kind, subject_key, name, value) DO NOTHING`,
		runID, subject, name, value); err != nil {
		t.Fatalf("recording observation %s: %v", name, err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	out := ""
	for i > 0 {
		out = string(rune('0'+i%10)) + out
		i /= 10
	}
	return out
}
