package store

import (
	"database/sql"
	"fmt"
)

// What an observation can be about.
//
// Named by key rather than row id so a query's output can be recorded whatever order subjects
// arrive in, and so a subject that is later re-ingested keeps the facts already recorded about it.
const (
	SubjectNode       = "node"
	SubjectEdge       = "edge"
	SubjectRepository = "repository"
)

// Observation is one thing a query said about one subject.
//
// This is the generic path for query output that is neither a dependency edge nor a security
// finding. ABOM's inspection engine is reprogrammable — the set of queries is expected to keep
// growing — so a new query has to be able to contribute facts without a schema change.
type Observation struct {
	SubjectKind string
	SubjectKey  string
	Name        string
	Value       string
	// Number is stored separately from Value so thresholds and ordering do not depend on string
	// collation. HasNumber tells an absent measurement from a measured zero.
	Number    float64
	HasNumber bool
	FilePath  string
	StartLine int
}

// StoredObservation is an observation read back, carrying the query that made it.
type StoredObservation struct {
	QueryID     string `json:"queryId"`
	SubjectKind string `json:"subjectKind"`
	SubjectKey  string `json:"subjectKey"`
	Name        string `json:"name"`
	Value       string `json:"value"`
	// HasNumber tells an absent measurement from a measured zero, so a reader is never shown a
	// 0 that nobody measured.
	Number    float64 `json:"number"`
	HasNumber bool    `json:"hasNumber"`
	FilePath  string  `json:"filePath"`
	StartLine int     `json:"startLine"`
}

// AddObservations records what a query said, returning how many rows were written.
//
// Re-running the same query against the same database refreshes its answers rather than adding
// to them, so a count drawn from this table cannot drift upward on its own.
func (s *Store) AddObservations(runID int64, obs []Observation) (int, error) {
	if len(obs) == 0 {
		return 0, nil
	}
	if runID == 0 {
		return 0, fmt.Errorf("store: observations need the query run that produced them")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	insert, err := tx.Prepare(`
		INSERT INTO abom_observation
			(query_run_id, subject_kind, subject_key, name, value, value_num, file_path, start_line)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(query_run_id, subject_kind, subject_key, name) DO UPDATE SET
			value = excluded.value,
			value_num = excluded.value_num,
			file_path = excluded.file_path,
			start_line = excluded.start_line`)
	if err != nil {
		return 0, fmt.Errorf("preparing observation insert: %w", err)
	}
	defer insert.Close()

	written := 0
	for _, o := range obs {
		if o.SubjectKey == "" || o.SubjectKind == "" || o.Name == "" {
			return 0, fmt.Errorf(
				"store: an observation needs a subject and a name, got kind=%q key=%q name=%q",
				o.SubjectKind, o.SubjectKey, o.Name)
		}
		var num any
		if o.HasNumber {
			num = o.Number
		}
		var line any
		if o.StartLine > 0 {
			line = o.StartLine
		}
		if _, err := insert.Exec(runID, o.SubjectKind, o.SubjectKey, o.Name,
			nullable(o.Value), num, nullable(o.FilePath), line); err != nil {
			return written, fmt.Errorf("recording observation %q: %w", o.Name, err)
		}
		written++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}

// ObservationsOf returns everything any query has said about one subject.
//
// Several queries may disagree; all of them are returned. Picking a winner is a judgement, and
// this layer records statements rather than making them.
func (s *Store) ObservationsOf(subjectKind, subjectKey string) ([]StoredObservation, error) {
	return queryAll(s, "reading observations", func(r *sql.Rows) (StoredObservation, error) {
		var o StoredObservation
		var num sql.NullFloat64
		var line sql.NullInt64
		err := r.Scan(&o.QueryID, &o.SubjectKind, &o.SubjectKey, &o.Name,
			&o.Value, &num, &o.FilePath, &line)
		o.Number, o.HasNumber = num.Float64, num.Valid
		o.StartLine = int(line.Int64)
		return o, err
	}, `
		SELECT q.query_id, o.subject_kind, o.subject_key, o.name,
		       COALESCE(o.value,''), o.value_num, COALESCE(o.file_path,''), o.start_line
		FROM abom_observation o
		JOIN abom_query_run q ON q.row_id = o.query_run_id
		WHERE o.subject_kind = ? AND o.subject_key = ?
		ORDER BY q.query_id, o.name`, subjectKind, subjectKey)
}

// QueryRunExists reports whether this exact query has already run against this exact database.
//
// CodeQL is deterministic, so the same query at the same version against the same commit cannot
// produce a different answer. Recognizing that is what makes re-processing affordable to offer:
// only the queries or databases that actually changed have to run again.
func (s *Store) QueryRunExists(q QueryRun) (bool, error) {
	if q.QueryID == "" || q.Repository == "" {
		return false, fmt.Errorf("store: QueryRun needs QueryID and Repository")
	}
	var one int
	err := s.db.QueryRow(
		`SELECT 1 FROM abom_query_run WHERE run_key = ?`, q.key()).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("looking up query run for %s: %w", q.QueryID, err)
	}
	return true, nil
}
