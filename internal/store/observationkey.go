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

// migrateObservationFactKey re-keys observations from the run that stated a fact to the fact
// itself, deduplicating what the old key let accumulate.
//
// The old UNIQUE led with `query_run_id`, which is new for every run, so re-analysis at a new
// commit or after a pack bump wrote a complete second copy of everything a query said. The
// reference corpus held 140,421 rows carrying 41,565 distinct facts — 69% redundant — and its
// only index led on that same column, which no read filters on.
//
// The surviving row of a duplicate set is the newest, because `loadConditions` orders by row id
// and lets the highest win; keeping the earliest would rewrite history to the first thing
// anybody said. Rows that disagree are not a duplicate set and all of them survive.
//
// SQLite cannot alter a UNIQUE constraint, hence the rebuild.
func (s *Store) migrateObservationFactKey() error {
	keyed, err := s.observationsKeyedByFact()
	if err != nil || keyed {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning the observation rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`ALTER TABLE abom_observation RENAME TO abom_observation_old`); err != nil {
		return fmt.Errorf("setting the old observations aside: %w", err)
	}
	if _, err := tx.Exec(observationDDL); err != nil {
		return fmt.Errorf("creating the re-keyed observation table: %w", err)
	}
	// A GROUP BY rollup rather than a correlated MAX per row: the same query shape that took the
	// frontier from 0.688 s to 0.122 s, and this runs over 140,421 rows on open.
	if _, err := tx.Exec(`INSERT INTO abom_observation (` + observationColumns + `)
		SELECT o.row_id, o.query_run_id, o.subject_kind, o.subject_key, o.name,
		       COALESCE(o.value, ''), o.value_num, o.file_path, o.start_line
		FROM abom_observation_old o
		JOIN (SELECT MAX(row_id) AS newest
		        FROM abom_observation_old
		       GROUP BY subject_kind, subject_key, name, COALESCE(value, '')) k
		  ON k.newest = o.row_id`); err != nil {
		return fmt.Errorf("deduplicating observations: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE abom_observation_old`); err != nil {
		return fmt.Errorf("dropping the old observations: %w", err)
	}
	// The index went with the old table, and both readers depend on one.
	if _, err := tx.Exec(observationIndexDDL); err != nil {
		return fmt.Errorf("recreating the observation index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Outside the transaction because VACUUM cannot run in one. Without it the dropped rows stay
	// on the freelist and the upgrade makes the file *bigger*: 80.82 MiB to 92.96 MiB on the
	// reference corpus, against 55.29 MiB once the 8,470 free pages are returned. It costs 0.38 s
	// and runs once, because the rebuild above never happens twice.
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("reclaiming the space the rebuild freed: %w", err)
	}
	return nil
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
