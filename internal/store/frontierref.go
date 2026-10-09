package store

import "fmt"

// frontierDDL is stated once because two things create this table: the schema, and the rebuild
// below that re-keys an existing database. A copy in either would let them drift.
const frontierDDL = `
CREATE TABLE IF NOT EXISTS abom_frontier (
  node_key        TEXT NOT NULL,
  -- The reference to check out. A dependency used at three refs is three units of work: three
  -- checkouts, three databases and three independent sets of results, whose findings need not
  -- agree. Empty means the default branch, which is what every row analyzed before this column
  -- existed was analyzed at.
  ref             TEXT NOT NULL DEFAULT '',
  owner           TEXT,
  repo            TEXT,
  subpath         TEXT,
  depth           INTEGER NOT NULL,
  status          TEXT NOT NULL,
  attempts        INTEGER NOT NULL DEFAULT 0,
  score           REAL NOT NULL DEFAULT 0,
  cost_estimate   REAL NOT NULL DEFAULT 1,
  reason          TEXT,
  discovered_from TEXT,
  enqueued_at     TEXT NOT NULL,
  next_attempt_at TEXT,
  completed_at    TEXT,
  -- When the item was handed to a worker. A crash cannot update a row, so this is the only
  -- evidence that distinguishes work in progress from work abandoned mid-flight.
  claimed_at      TEXT,
  -- When someone asked for this specific dependency ahead of the ranking. Named "expedited"
  -- rather than "pinned" because a pinned reference already means a SHA-pinned uses: clause
  -- everywhere else here, and the two would read as the same thing.
  expedited_at    TEXT,
  -- What was analyzed, and what it pointed at when it was analyzed. Mutable refs move, so a
  -- recorded SHA is the only way to tell whether the graph still describes the current code.
  -- resolved_sha always names the commit the stored graph came FROM, never a newer one.
  analyzed_ref    TEXT,
  resolved_sha    TEXT,
  analyzed_at     TEXT,
  -- checked_at tracks the cheap ref probe, which runs far more often than a full re-analysis.
  checked_at      TEXT,
  analysis_count  INTEGER NOT NULL DEFAULT 0,
  -- Whether anything under review executes this, or it only runs in an upstream project's own
  -- CI. Both are followed, because a compromised release pipeline is how consumers receive a
  -- malicious version, but they are different claims and must not be reported as one.
  supply_chain    TEXT NOT NULL DEFAULT 'runtime',
  -- Why a row analyzed before is pending again, which decides its stage: 'moved' waits behind
  -- everything never analyzed, 'reobserve' is a gap in what is known and goes with the first
  -- stage. NULL for anything else, and cleared when the row settles.
  requeued_for    TEXT,
  -- What dependency review said of a moved row's direct Actions dependencies between the commit it
  -- was analyzed at and the live one: 1 moved, 0 saw none, NULL not read. It orders pushes and
  -- describes one pending spell, so it is cleared when the row settles.
  dependencies_moved INTEGER,
  -- The pushed commit a pending row waits for code scanning to analyze, its ceiling in
  -- next_attempt_at; NULL when nothing holds it. Cleared when the row is claimed or settles.
  awaiting_code_scanning TEXT,
  PRIMARY KEY (node_key, ref)
);
`

// frontierIndexDDL is separate because the rebuild drops the table and its indexes with it.
const frontierIndexDDL = `
CREATE INDEX IF NOT EXISTS idx_frontier_claim  ON abom_frontier(status, score);
CREATE INDEX IF NOT EXISTS idx_frontier_check  ON abom_frontier(status, checked_at);
`

// frontierColumns is the copy order for the rebuild below, written out rather than `SELECT *`
// because the old table's column order is creation order plus whatever was appended by
// `addedColumns`, which does not match the DDL.
const frontierColumns = `node_key, ref, owner, repo, subpath, depth, status, attempts, score,
	cost_estimate, reason, discovered_from, enqueued_at, next_attempt_at, completed_at,
	claimed_at, expedited_at, analyzed_ref, resolved_sha, analyzed_at, checked_at,
	analysis_count, supply_chain, requeued_for, dependencies_moved, awaiting_code_scanning`

// migrateFrontierRefKey re-keys the queue from the dependency to the dependency at a reference.
//
// A database created before this has `node_key` alone as its primary key, which permits exactly
// one row per dependency and so exactly one analyzed tree — the default branch. Two refs are two
// checkouts and two independent sets of results, so the key has to admit both. SQLite cannot
// alter a primary key, hence the rebuild.
//
// Existing rows keep `ref = ”`, meaning the default branch, which is what they were in fact
// analyzed at. Nothing is re-analyzed by the migration itself.
func (s *Store) migrateFrontierRefKey() error {
	keyed, err := s.frontierKeyedByRef()
	if err != nil || keyed {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning the frontier rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`ALTER TABLE abom_frontier RENAME TO abom_frontier_old`); err != nil {
		return fmt.Errorf("setting the old frontier aside: %w", err)
	}
	if _, err := tx.Exec(frontierDDL); err != nil {
		return fmt.Errorf("creating the re-keyed frontier: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO abom_frontier (` + frontierColumns + `)
		SELECT ` + frontierColumns + ` FROM abom_frontier_old`); err != nil {
		return fmt.Errorf("copying the frontier forward: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE abom_frontier_old`); err != nil {
		return fmt.Errorf("dropping the old frontier: %w", err)
	}
	// The indexes went with the old table, and the claim order is the queue's hot path.
	if _, err := tx.Exec(frontierIndexDDL); err != nil {
		return fmt.Errorf("recreating the frontier indexes: %w", err)
	}
	return tx.Commit()
}

// frontierKeyedByRef reports whether `ref` is already part of the primary key. The column alone
// is not enough: `addedColumns` adds it to the old table, where it is not part of the key and a
// second ref would still collide.
func (s *Store) frontierKeyedByRef() (bool, error) {
	rows, err := s.db.Query(`SELECT name, pk FROM pragma_table_info('abom_frontier')`)
	if err != nil {
		return false, fmt.Errorf("reading the frontier's key: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var pk int
		if err := rows.Scan(&name, &pk); err != nil {
			return false, err
		}
		if name == "ref" && pk > 0 {
			return true, rows.Err()
		}
	}
	return false, rows.Err()
}
