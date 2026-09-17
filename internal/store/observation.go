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

// ObservationCondition is the fact AbomStepConditions.ql states: the `if:` guarding a step,
// verbatim. Named here because both the query that emits it and the traversal that joins it
// have to mean the same string.
const ObservationCondition = "condition"

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
// A statement already on record is not written again, whichever run made it: the row is keyed on
// the fact and its value, so re-analysis at a new commit refreshes attribution rather than adding
// a copy. Until 2026-09-11 the key led with the run, and this comment promised the opposite of
// what the table did — the reference corpus had accumulated 3.38 copies of the average fact.
//
// Two runs that state *different* values are two statements and both are kept, because choosing
// between them is a judgement this layer does not make.
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
		ON CONFLICT(subject_kind, subject_key, name, value) DO UPDATE SET
			query_run_id = excluded.query_run_id,
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
			o.Value, num, nullable(o.FilePath), line); err != nil {
			return written, fmt.Errorf("recording observation %q: %w", o.Name, err)
		}
		written++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}

// observationsOfQuery is named so its plan can be asserted. It filters by subject, which is what
// the fact key leads with.
const observationsOfQuery = `
		SELECT q.query_id, o.subject_kind, o.subject_key, o.name,
		       o.value, o.value_num, COALESCE(o.file_path,''), o.start_line
		FROM abom_observation o
		JOIN abom_query_run q ON q.row_id = o.query_run_id
		WHERE o.subject_kind = ? AND o.subject_key = ?
		ORDER BY q.query_id, o.name`

// ConditionObservationsQuery reads every step condition on record, newest last.
//
// It lives here rather than in the traversal that consumes it because it is the second reader of
// this table and the one the fact key cannot serve: it asks by attribute name across all
// subjects, where UNIQUE leads with the subject. `idx_observation_name` exists for this query,
// and keeping the text beside the schema is what stops the two drifting apart.
const ConditionObservationsQuery = `
		SELECT n.row_id, o.value
		FROM abom_observation o
		JOIN abom_node_live n ON n.node_key = o.subject_key
		WHERE o.subject_kind = ? AND o.name = ?
		ORDER BY o.row_id`

// ObservationsOf returns everything any query has said about one subject.
//
// Several queries may disagree; all of them are returned. Picking a winner is a judgement, and
// this layer records statements rather than making them. What is *not* returned is the same
// statement twice: re-confirmation refreshes the row rather than adding one.
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
	}, observationsOfQuery, subjectKind, subjectKey)
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
