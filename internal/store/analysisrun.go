package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// What became of one repository in a run.
//
// Every one of these is a member that is kept. The vocabulary is sarif-sql's, which recorded a
// repository's outcome whatever it was rather than only the successes: downstream can then say
// *why* something is missing, which a set holding only what worked cannot.
const (
	AnalysisPending    = "pending"
	AnalysisRunning    = "running"
	AnalysisSucceeded  = "succeeded"
	AnalysisFailed     = "failed"
	AnalysisTimedOut   = "timeout"
	AnalysisNoDatabase = "no_database"
	AnalysisEvicted    = "evicted"
	// AnalysisSkipped carries the answer of an evaluation already stored under the same run key.
	AnalysisSkipped = "skipped"
)

// What became of a run as a whole.
const (
	RunPending  = "pending"
	RunRunning  = "running"
	RunComplete = "complete"
	RunCanceled = "canceled"
	// RunInterrupted is a run the server stopped under, which nobody canceled and nothing finished.
	RunInterrupted = "interrupted"
	// RunRefused is a run that could not start, such as one naming a query no suite offers, and it
	// says why.
	RunRefused = "refused"
)

// AnalysisQuery is one query a run names.
type AnalysisQuery struct {
	QueryID      string `json:"queryId"`
	QueryVersion string `json:"queryVersion"`
}

// AnalysisMember is one repository in a run, and what became of it.
type AnalysisMember struct {
	DatabaseIdentity
	Status       string `json:"status"`
	Detail       string `json:"detail"`
	DatabasePath string `json:"databasePath"`
	ResultPath   string `json:"resultPath"`
	// Results is meaningful only where Counted is true. A member nothing ran against has no
	// count, and reporting its zero would read as a repository a query found nothing in.
	Results int  `json:"results"`
	Counted bool `json:"counted"`
	// ByQuery is what each query found here, empty for a member that was not counted.
	ByQuery    map[string]int `json:"byQuery"`
	StartedAt  string         `json:"startedAt"`
	FinishedAt string         `json:"finishedAt"`
}

// MemberResult is what became of one member once it stopped.
type MemberResult struct {
	Status     string
	Detail     string
	ResultPath string
	Results    int
	Counted    bool
	ByQuery    map[string]int
	// QueryRunID is the ledger row the member's results were stored under, zero when none were.
	QueryRunID int64
}

// AnalysisRun is a request, its members and how far it has got.
type AnalysisRun struct {
	RowID       int64  `json:"rowId"`
	Predicate   string `json:"predicate"`
	Mode        string `json:"mode"`
	RequestedBy string `json:"requestedBy"`
	State       string `json:"state"`
	// Prediction is a run whose results are held as what its queries would report, never stored as
	// findings.
	Prediction bool   `json:"prediction"`
	CreatedAt  string `json:"createdAt"`
	FinishedAt string `json:"finishedAt"`
	// CLIVersion is the CodeQL that carried the run out, empty until it starts.
	CLIVersion string `json:"cliVersion"`
	// Refusal is why the run could not start the last time it was asked to, and empty otherwise.
	Refusal string           `json:"refusal"`
	Queries []AnalysisQuery  `json:"queries"`
	Members []AnalysisMember `json:"members"`
	// Results totals what the members that were counted reported. Members nothing ran against
	// contribute nothing to it rather than a zero.
	Results int `json:"results"`
	// Tally counts every member by status, so a reader deciding what to do next is not left to
	// infer the missing ones from a total.
	Tally map[string]int `json:"tally"`
}

// AnalysisRequest is what somebody asked for.
type AnalysisRequest struct {
	RequestedBy string
	Predicate   string
	Mode        string
	Prediction  bool
	Queries     []AnalysisQuery
	// Databases and Gaps are what the predicate resolved to, taken straight from the resolution
	// /api/databases previews, so the run is over exactly the set a reader was shown.
	Databases []ScopedDatabase
	Gaps      []DatabaseGap
}

// CreateAnalysisRun records a request and every repository it covers.
//
// The gaps become members too. A run holding only the analyzable half would report a clean result
// for repositories nothing ran against, which is the absent-is-not-zero failure arriving at the
// one place a reader is about to act on it.
func (s *Store) CreateAnalysisRun(req AnalysisRequest) (int64, error) {
	mode := req.Mode
	if mode == "" {
		mode = AnalysisModeRegular
	}
	switch {
	case req.RequestedBy == "":
		return 0, fmt.Errorf("starting a run: no one is recorded as asking for it")
	case len(req.Queries) == 0:
		return 0, fmt.Errorf("starting a run: no query named, so it would analyze nothing")
	case len(req.Databases)+len(req.Gaps) == 0:
		return 0, fmt.Errorf("starting a run: the scope named no repository")
	case !ValidAnalysisMode(mode):
		return 0, fmt.Errorf("starting a run: %q is not an analysis mode", mode)
	case req.Prediction && mode != AnalysisModeRegular:
		return 0, fmt.Errorf("predicting from %s databases: a prediction is made from regular ones "+
			"only, because Default Setup builds one repository's own checkout and stages nothing, so "+
			"it would never report what %s analysis reveals", mode, mode)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(`
		INSERT INTO abom_analysis_run
			(predicate, analysis_mode, requested_by, state, created_at, prediction)
		VALUES (?, ?, ?, ?, ?, ?)`,
		req.Predicate, mode, req.RequestedBy, RunPending, now, req.Prediction)
	if err != nil {
		return 0, fmt.Errorf("recording the run: %w", err)
	}
	// A plain INSERT, so LastInsertId is the row it just wrote.
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading back the run: %w", err)
	}

	for _, q := range req.Queries {
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO abom_analysis_query (run_id, query_id, query_version)
			VALUES (?, ?, ?)`, id, q.QueryID, q.QueryVersion); err != nil {
			return 0, fmt.Errorf("recording query %s: %w", q.QueryID, err)
		}
	}
	for _, d := range req.Databases {
		status, detail := AnalysisPending, ""
		switch {
		case d.Status != DatabaseBuildable:
		case mode != AnalysisModeRegular:
			status = AnalysisNoDatabase
			detail = fmt.Sprintf("no %s database held, and a run builds only regular ones", mode)
		default:
			detail = "no database held; the run builds one"
		}
		if _, err := insertMember(tx, id, d.DatabaseIdentity, status, detail, d.Path); err != nil {
			return 0, err
		}
	}
	for _, g := range req.Gaps {
		identity := DatabaseIdentity{Repository: g.Repository, Mode: mode}
		status, detail, err := gapMember(tx, g, mode)
		if err != nil {
			return 0, err
		}
		if _, err := insertMember(tx, id, identity, status, detail, ""); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing the run: %w", err)
	}
	return id, nil
}

// gapMember decides whether the run builds a repository nothing has analyzed: only one ABOM
// tracks, since ingesting any other would enter it as a seed, and only in the crawl's own shape.
func gapMember(tx *sql.Tx, g DatabaseGap, mode string) (string, string, error) {
	var tracked int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM repository WHERE repository_full_name = ?`,
		g.Repository).Scan(&tracked); err != nil {
		return "", "", fmt.Errorf("looking up %s: %w", g.Repository, err)
	}
	switch {
	case tracked == 0:
		return AnalysisNoDatabase, fmt.Sprintf(
			"ABOM tracks no repository named %s, so a run does not fetch it", g.Repository), nil
	case mode != AnalysisModeRegular:
		return AnalysisNoDatabase, g.Reason, nil
	}
	return AnalysisPending, "no regular analysis on record; the run builds its default branch", nil
}

func insertMember(
	tx *sql.Tx, runID int64, id DatabaseIdentity, status, detail, path string,
) (sql.Result, error) {
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO abom_analysis_member
			(run_id, repository, db_commit, source_filter, analysis_mode, status, detail,
			 database_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, id.Repository, id.Commit, id.SourceFilter, id.Mode, status, detail, path)
	if err != nil {
		return nil, fmt.Errorf("recording %s in the run: %w", id.Repository, err)
	}
	return res, nil
}

// SetAnalysisMemberStatus moves one member, recording when it started and when it stopped.
//
// A result path is written only when one is given, so moving a member to `running` and then to
// `succeeded` does not blank what the first call recorded.
func (s *Store) SetAnalysisMemberStatus(
	runID int64, id DatabaseIdentity, status, detail string,
) error {
	return s.setMember(runID, id, MemberResult{Status: status, Detail: detail})
}

// RecordAnalysisMemberResult moves a member, names where its raw output was written and says how
// much it found.
func (s *Store) RecordAnalysisMemberResult(
	runID int64, id DatabaseIdentity, r MemberResult,
) error {
	return s.setMember(runID, id, r)
}

// SetAnalysisMemberDatabase records the database a member was built at, which is the identity its
// results are then stored under. An empty path is a database nothing retains.
func (s *Store) SetAnalysisMemberDatabase(runID int64, from, to DatabaseIdentity, path string) error {
	if from.Repository != to.Repository || from.Mode != to.Mode {
		return fmt.Errorf("moving %s in run %d: a member stays the repository and shape it was",
			from.Repository, runID)
	}
	res, err := s.db.Exec(`
		UPDATE abom_analysis_member SET db_commit = ?, source_filter = ?, database_path = ?
		WHERE run_id = ? AND repository = ? AND db_commit = ?
		  AND source_filter = ? AND analysis_mode = ?`,
		to.Commit, to.SourceFilter, path,
		runID, from.Repository, from.Commit, from.SourceFilter, from.Mode)
	if err != nil {
		return fmt.Errorf("moving %s in run %d: %w", from.Repository, runID, err)
	}
	moved, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("moving %s in run %d: %w", from.Repository, runID, err)
	}
	if moved != 1 {
		return fmt.Errorf("run %d has no member %s at %q", runID, from.Repository, from.Commit)
	}
	return nil
}

// AnsweredBy reports the run member whose stored evaluation answers this run key, and what it
// found. Only a counted member linked to the key's ledger row answers: a row alone is what a
// failed ingest leaves behind.
func (s *Store) AnsweredBy(q QueryRun) (int64, MemberResult, bool, error) {
	if err := q.identified(); err != nil {
		return 0, MemberResult{}, false, err
	}
	var runID, results int64
	var at DatabaseIdentity
	r := MemberResult{ByQuery: map[string]int{}, Counted: true}
	err := s.db.QueryRow(`
		SELECT m.run_id, m.query_run_id, m.result_path, m.result_count,
		       m.repository, m.db_commit, m.source_filter, m.analysis_mode
		FROM abom_query_run q
		JOIN abom_analysis_member m ON m.query_run_id = q.row_id
		WHERE q.run_key = ? AND m.status IN (?, ?) AND m.result_count IS NOT NULL
		ORDER BY m.run_id DESC LIMIT 1`, q.key(), AnalysisSucceeded, AnalysisSkipped).
		Scan(&runID, &r.QueryRunID, &r.ResultPath, &results,
			&at.Repository, &at.Commit, &at.SourceFilter, &at.Mode)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, MemberResult{}, false, nil
	}
	if err != nil {
		return 0, MemberResult{}, false, fmt.Errorf("looking for what answers %s: %w", q.QueryID, err)
	}
	r.Results = int(results)

	rows, err := s.db.Query(`
		SELECT query_id, result_count FROM abom_analysis_result
		WHERE run_id = ? AND repository = ? AND db_commit = ? AND source_filter = ?
		  AND analysis_mode = ?`, runID, at.Repository, at.Commit, at.SourceFilter, at.Mode)
	if err != nil {
		return 0, MemberResult{}, false, fmt.Errorf("reading what run %d found: %w", runID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var query string
		var n int
		if err := rows.Scan(&query, &n); err != nil {
			return 0, MemberResult{}, false, fmt.Errorf("scanning what run %d found: %w", runID, err)
		}
		r.ByQuery[query] = n
	}
	return runID, r, true, rows.Err()
}

func (s *Store) setMember(runID int64, id DatabaseIdentity, r MemberResult) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if r.QueryRunID != 0 {
		predicted, err := runIsPrediction(tx, runID)
		if err != nil {
			return err
		}
		// Linked, the prediction's answer would let a later run skip storing what it found.
		if predicted {
			return fmt.Errorf("run %d is a prediction, which stores no evaluation of %s",
				runID, id.Repository)
		}
	}
	if err := updateMember(tx, runID, id, r); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing %s: %w", id.Repository, err)
	}
	return nil
}

func updateMember(tx *sql.Tx, runID int64, id DatabaseIdentity, r MemberResult) error {
	now := time.Now().UTC().Format(time.RFC3339)
	started, finished := nullable(""), nullable("")
	if r.Status == AnalysisRunning {
		started = now
	}
	if r.Status != AnalysisPending && r.Status != AnalysisRunning {
		finished = now
	}
	// NULL unless this call counted: COALESCE then keeps whatever an earlier one recorded rather
	// than overwriting a real count with an unmeasured zero.
	var results any
	if r.Counted {
		results = r.Results
	}

	if _, err := tx.Exec(`
		UPDATE abom_analysis_member
		SET status = ?, detail = ?,
		    result_path = CASE WHEN ? <> '' THEN ? ELSE result_path END,
		    result_count = COALESCE(?, result_count),
		    query_run_id = COALESCE(?, query_run_id),
		    started_at = COALESCE(?, started_at),
		    finished_at = COALESCE(?, finished_at)
		WHERE run_id = ? AND repository = ? AND db_commit = ?
		  AND source_filter = ? AND analysis_mode = ?`,
		r.Status, r.Detail, r.ResultPath, r.ResultPath, results, nullableID(r.QueryRunID),
		started, finished, runID, id.Repository, id.Commit, id.SourceFilter, id.Mode); err != nil {
		return fmt.Errorf("moving %s to %s: %w", id.Repository, r.Status, err)
	}
	if r.Counted {
		return replaceMemberResults(tx, runID, id, r.ByQuery)
	}
	return nil
}

func runIsPrediction(tx *sql.Tx, runID int64) (bool, error) {
	var predicted bool
	err := tx.QueryRow(`SELECT prediction FROM abom_analysis_run WHERE row_id = ?`, runID).Scan(&predicted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("no run %d", runID)
	}
	if err != nil {
		return false, fmt.Errorf("reading run %d: %w", runID, err)
	}
	return predicted, nil
}

// replaceMemberResults replaces rather than adds, or a re-run would double every count.
func replaceMemberResults(tx *sql.Tx, runID int64, id DatabaseIdentity, byQuery map[string]int) error {
	if _, err := tx.Exec(`
		DELETE FROM abom_analysis_result
		WHERE run_id = ? AND repository = ? AND db_commit = ?
		  AND source_filter = ? AND analysis_mode = ?`,
		runID, id.Repository, id.Commit, id.SourceFilter, id.Mode); err != nil {
		return fmt.Errorf("clearing what %s found: %w", id.Repository, err)
	}
	for query, n := range byQuery {
		if _, err := tx.Exec(`
			INSERT INTO abom_analysis_result
				(run_id, repository, db_commit, source_filter, analysis_mode, query_id, result_count)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			runID, id.Repository, id.Commit, id.SourceFilter, id.Mode, query, n); err != nil {
			return fmt.Errorf("recording what %s found in %s: %w", query, id.Repository, err)
		}
	}
	return nil
}

// SetAnalysisRunCodeQL records the CodeQL carrying a run out, which every member it evaluates
// answers under.
func (s *Store) SetAnalysisRunCodeQL(runID int64, cli string) error {
	if cli == "" {
		return fmt.Errorf("run %d: a run names the CodeQL that carries it out", runID)
	}
	if _, err := s.db.Exec(`UPDATE abom_analysis_run SET cli_version = ? WHERE row_id = ?`,
		cli, runID); err != nil {
		return fmt.Errorf("recording the CodeQL of run %d: %w", runID, err)
	}
	return nil
}

// RefuseAnalysisRun records that a run could not start, why, and when.
func (s *Store) RefuseAnalysisRun(runID int64, reason string) error {
	_, err := s.db.Exec(`
		UPDATE abom_analysis_run SET state = ?, refusal = ?, finished_at = ? WHERE row_id = ?`,
		RunRefused, reason, time.Now().UTC().Format(time.RFC3339), runID)
	if err != nil {
		return fmt.Errorf("refusing run %d: %w", runID, err)
	}
	return nil
}

// SetAnalysisRunState moves the run as a whole, stamping when it finished. A refusal describes the
// state it was recorded with, so moving on clears it.
func (s *Store) SetAnalysisRunState(runID int64, state string) error {
	finished := nullable("")
	if state == RunComplete || state == RunCanceled {
		finished = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := s.db.Exec(`
		UPDATE abom_analysis_run
		SET state = ?, finished_at = COALESCE(?, finished_at), refusal = NULL
		WHERE row_id = ?`, state, finished, runID)
	if err != nil {
		return fmt.Errorf("moving run %d to %s: %w", runID, state, err)
	}
	return nil
}

// AnalysisRun reads one run with its queries and every member.
func (s *Store) AnalysisRun(id int64) (AnalysisRun, error) {
	run := AnalysisRun{
		Queries: []AnalysisQuery{}, Members: []AnalysisMember{}, Tally: map[string]int{},
	}
	var finished sql.NullString
	err := s.db.QueryRow(`
		SELECT row_id, predicate, analysis_mode, requested_by, state, created_at, finished_at,
		       COALESCE(cli_version, ''), COALESCE(refusal, ''), prediction
		FROM abom_analysis_run WHERE row_id = ?`, id).
		Scan(&run.RowID, &run.Predicate, &run.Mode, &run.RequestedBy, &run.State,
			&run.CreatedAt, &finished, &run.CLIVersion, &run.Refusal, &run.Prediction)
	if errors.Is(err, sql.ErrNoRows) {
		return run, fmt.Errorf("no run %d", id)
	}
	if err != nil {
		return run, fmt.Errorf("reading run %d: %w", id, err)
	}
	run.FinishedAt = finished.String

	if run.Queries, err = s.runQueriesOf(id); err != nil {
		return run, err
	}
	if run.Members, err = s.runMembersOf(id); err != nil {
		return run, err
	}
	for _, m := range run.Members {
		run.Tally[m.Status]++
		run.Results += m.Results
	}
	return run, nil
}

// AnalysisRuns lists every run, newest first, without their members.
//
// Without the members deliberately: a list of runs over a thousand-repository corpus would carry
// a thousand rows each, and the list is read to choose one.
func (s *Store) AnalysisRuns() ([]AnalysisRun, error) {
	out := []AnalysisRun{}
	rows, err := s.db.Query(`
		SELECT row_id, predicate, analysis_mode, requested_by, state, created_at, finished_at,
		       COALESCE(cli_version, ''), COALESCE(refusal, ''), prediction
		FROM abom_analysis_run ORDER BY row_id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		run := AnalysisRun{
			Queries: []AnalysisQuery{}, Members: []AnalysisMember{}, Tally: map[string]int{},
		}
		var finished sql.NullString
		if err := rows.Scan(&run.RowID, &run.Predicate, &run.Mode, &run.RequestedBy,
			&run.State, &run.CreatedAt, &finished, &run.CLIVersion, &run.Refusal,
			&run.Prediction); err != nil {
			return nil, fmt.Errorf("scanning a run: %w", err)
		}
		run.FinishedAt = finished.String
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}

	// The tally and the queries per run: the first is progress, the second is what a reader picks
	// a run out by. Members stay out — they are the rows this list refuses to carry.
	for i := range out {
		tally, err := s.runTallyOf(out[i].RowID)
		if err != nil {
			return nil, err
		}
		out[i].Tally = tally

		queries, err := s.runQueriesOf(out[i].RowID)
		if err != nil {
			return nil, err
		}
		out[i].Queries = queries

		// SUM over an empty set is NULL, and a run whose members have all not run yet is exactly
		// that set, so the total is read through a nullable rather than scanned into an int.
		var total sql.NullInt64
		if err := s.db.QueryRow(`
			SELECT SUM(result_count) FROM abom_analysis_member WHERE run_id = ?`,
			out[i].RowID).Scan(&total); err != nil {
			return nil, fmt.Errorf("totalling the findings of run %d: %w", out[i].RowID, err)
		}
		out[i].Results = int(total.Int64)
	}
	return out, nil
}

func (s *Store) runQueriesOf(id int64) ([]AnalysisQuery, error) {
	out := []AnalysisQuery{}
	rows, err := s.db.Query(`
		SELECT query_id, query_version FROM abom_analysis_query
		WHERE run_id = ? ORDER BY query_id, query_version`, id)
	if err != nil {
		return nil, fmt.Errorf("reading the queries of run %d: %w", id, err)
	}
	defer rows.Close()

	for rows.Next() {
		var q AnalysisQuery
		if err := rows.Scan(&q.QueryID, &q.QueryVersion); err != nil {
			return nil, fmt.Errorf("scanning a run query: %w", err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *Store) runMembersOf(id int64) ([]AnalysisMember, error) {
	out := []AnalysisMember{}
	rows, err := s.db.Query(`
		SELECT repository, db_commit, source_filter, analysis_mode, status, detail,
		       database_path, result_path, result_count,
		       COALESCE(started_at, ''), COALESCE(finished_at, '')
		FROM abom_analysis_member WHERE run_id = ? ORDER BY repository, db_commit`, id)
	if err != nil {
		return nil, fmt.Errorf("reading the members of run %d: %w", id, err)
	}
	defer rows.Close()

	for rows.Next() {
		m := AnalysisMember{ByQuery: map[string]int{}}
		var results sql.NullInt64
		if err := rows.Scan(&m.Repository, &m.Commit, &m.SourceFilter, &m.Mode,
			&m.Status, &m.Detail, &m.DatabasePath, &m.ResultPath, &results,
			&m.StartedAt, &m.FinishedAt); err != nil {
			return nil, fmt.Errorf("scanning a run member: %w", err)
		}
		m.Results, m.Counted = int(results.Int64), results.Valid
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.attachMemberResults(id, out)
}

func (s *Store) attachMemberResults(id int64, members []AnalysisMember) error {
	index := make(map[DatabaseIdentity]int, len(members))
	for i, m := range members {
		index[m.DatabaseIdentity] = i
	}
	rows, err := s.db.Query(`
		SELECT repository, db_commit, source_filter, analysis_mode, query_id, result_count
		FROM abom_analysis_result WHERE run_id = ?`, id)
	if err != nil {
		return fmt.Errorf("reading what run %d found per query: %w", id, err)
	}
	defer rows.Close()

	for rows.Next() {
		var key DatabaseIdentity
		var query string
		var n int
		if err := rows.Scan(&key.Repository, &key.Commit, &key.SourceFilter, &key.Mode,
			&query, &n); err != nil {
			return fmt.Errorf("scanning a per-query count: %w", err)
		}
		if i, ok := index[key]; ok {
			members[i].ByQuery[query] = n
		}
	}
	return rows.Err()
}

func (s *Store) runTallyOf(id int64) (map[string]int, error) {
	tally := map[string]int{}
	rows, err := s.db.Query(`
		SELECT status, COUNT(*) FROM abom_analysis_member WHERE run_id = ? GROUP BY status`, id)
	if err != nil {
		return nil, fmt.Errorf("tallying run %d: %w", id, err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scanning a tally: %w", err)
		}
		tally[status] = n
	}
	return tally, rows.Err()
}
