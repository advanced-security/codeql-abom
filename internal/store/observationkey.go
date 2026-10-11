package store

import "fmt"

// observationDDL is stated once because two things create this table: the schema, and the
// rebuild that re-keys it.
const observationDDL = `
CREATE TABLE IF NOT EXISTS abom_observation (
  row_id       INTEGER PRIMARY KEY,
  query_run_id INTEGER NOT NULL REFERENCES abom_query_run(row_id),
  subject_kind TEXT NOT NULL,
  subject_key  TEXT NOT NULL,
  name         TEXT NOT NULL,
  -- NOT NULL because NULL never equals NULL in a UNIQUE constraint, so a nullable value could
  -- not deduplicate against itself. Both readers already COALESCE it, so absent and empty were
  -- indistinguishable on the way out and distinct on the way in.
  value        TEXT NOT NULL DEFAULT '',
  -- Kept apart from value so ordering and thresholds do not depend on string collation.
  value_num    REAL,
  file_path    TEXT,
  start_line   INTEGER,
  -- One row per distinct statement, not per run that repeated it. query_run_id led this key
  -- until 2026-09-11 and is new for every run, so re-analysis wrote a complete second copy:
  -- 140,421 rows carrying 41,565 facts on the reference corpus. Keyed on the value rather than
  -- on the fact, because two runs that genuinely disagree are two statements and choosing
  -- between them is a judgement this layer does not make.
  UNIQUE(subject_kind, subject_key, name, value)
);`

// observationColumns is spelled out because the rebuild copies row ids forward, and the
// traversal reads the highest one.
const observationColumns = `row_id, query_run_id, subject_kind, subject_key, name,
	value, value_num, file_path, start_line`

// migrateObservationFactKey re-keys observations from runs to facts.
// The newest duplicate survives because `loadConditions` lets the highest row id win.
// SQLite cannot alter a UNIQUE constraint, hence the rebuild.
func (s *Store) migrateObservationFactKey() (bool, error) {
	keyed, err := s.observationsKeyedByFact()
	if err != nil || keyed {
		return false, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("beginning the observation rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`ALTER TABLE abom_observation RENAME TO abom_observation_old`); err != nil {
		return false, fmt.Errorf("setting the old observations aside: %w", err)
	}
	if _, err := tx.Exec(observationDDL); err != nil {
		return false, fmt.Errorf("creating the re-keyed observation table: %w", err)
	}
	// A GROUP BY rollup avoids a correlated MAX per row during open.
	if _, err := tx.Exec(`INSERT INTO abom_observation (` + observationColumns + `)
		SELECT o.row_id, o.query_run_id, o.subject_kind, o.subject_key, o.name,
		       COALESCE(o.value, ''), o.value_num, o.file_path, o.start_line
		FROM abom_observation_old o
		JOIN (SELECT MAX(row_id) AS newest
		        FROM abom_observation_old
		       GROUP BY subject_kind, subject_key, name, COALESCE(value, '')) k
		  ON k.newest = o.row_id`); err != nil {
		return false, fmt.Errorf("deduplicating observations: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE abom_observation_old`); err != nil {
		return false, fmt.Errorf("dropping the old observations: %w", err)
	}
	// The index went with the old table, and both readers depend on one.
	if _, err := tx.Exec(observationIndexDDL); err != nil {
		return false, fmt.Errorf("recreating the observation index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// observationsKeyedByFact reports whether the table is already keyed on the fact rather than on
// the run that stated it. The unique index's first column is the whole question: `query_run_id`
// leading it is what let re-runs accumulate.
func (s *Store) observationsKeyedByFact() (bool, error) {
	rows, err := s.db.Query(`
		SELECT il.name
		FROM pragma_index_list('abom_observation') il
		JOIN pragma_index_info(il.name) ii
		WHERE il."unique" = 1 AND ii.seqno = 0 AND ii.name = 'query_run_id'`)
	if err != nil {
		return false, fmt.Errorf("reading the observation key: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		return false, rows.Err()
	}
	return true, rows.Err()
}
