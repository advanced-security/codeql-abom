package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DatabaseIdentity names one CodeQL database on this disk.
//
// The same tuple abom_query_run hashes into its run key, deliberately: "which database" has one
// definition, or the cache and the ledger drift into disagreeing about what has been analyzed.
type DatabaseIdentity struct {
	Repository   string `json:"repository"`
	Commit       string `json:"commit"`
	SourceFilter string `json:"sourceFilter"`
	Mode         string `json:"mode"`
}

// RetainedDatabase is a database held on disk and what it costs to keep.
type RetainedDatabase struct {
	DatabaseIdentity
	RowID      int64  `json:"rowId"`
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	CLIVersion string `json:"cliVersion"`
	BuiltAt    string `json:"builtAt"`
	LastUsedAt string `json:"lastUsedAt"`
}

const retainedDatabaseColumns = `row_id, repository, db_commit, source_filter, analysis_mode,
	path, bytes, COALESCE(cli_version, ''), built_at, last_used_at`

func scanRetainedDatabase(row interface{ Scan(...any) error }) (RetainedDatabase, error) {
	var d RetainedDatabase
	err := row.Scan(&d.RowID, &d.Repository, &d.Commit, &d.SourceFilter, &d.Mode,
		&d.Path, &d.Bytes, &d.CLIVersion, &d.BuiltAt, &d.LastUsedAt)
	return d, err
}

// RetainDatabase records a database that is now on disk, replacing whatever was held at the same
// identity.
//
// A rebuild at one identity must replace rather than accumulate, or the budget would be computed
// from directories that no longer exist and eviction would free nothing.
func (s *Store) RetainDatabase(d RetainedDatabase) (int64, error) {
	switch {
	case d.Repository == "":
		return 0, fmt.Errorf("retaining a database: no repository named")
	case d.Path == "":
		return 0, fmt.Errorf("retaining %s: no path named", d.Repository)
	case !ValidAnalysisMode(d.Mode):
		return 0, fmt.Errorf("retaining %s: %q is not an analysis mode", d.Repository, d.Mode)
	case d.Bytes < 0:
		return 0, fmt.Errorf("retaining %s: %d bytes", d.Repository, d.Bytes)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if d.BuiltAt == "" {
		d.BuiltAt = now
	}
	if d.LastUsedAt == "" {
		d.LastUsedAt = now
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	seq, err := nextUseSeq(tx)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`
		INSERT INTO abom_local_database
			(repository, db_commit, source_filter, analysis_mode, path, bytes,
			 cli_version, built_at, last_used_at, used_seq)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (repository, db_commit, source_filter, analysis_mode) DO UPDATE SET
			path = excluded.path,
			bytes = excluded.bytes,
			cli_version = excluded.cli_version,
			built_at = excluded.built_at,
			last_used_at = excluded.last_used_at,
			used_seq = excluded.used_seq`,
		d.Repository, d.Commit, d.SourceFilter, d.Mode, d.Path, d.Bytes,
		nullable(d.CLIVersion), d.BuiltAt, d.LastUsedAt, seq); err != nil {
		return 0, fmt.Errorf("retaining %s@%s: %w", d.Repository, d.Commit, err)
	}

	// Read the id back. An upsert reports one row affected even when it updated, and leaves
	// LastInsertId on an unrelated row.
	var id int64
	if err := tx.QueryRow(`
		SELECT row_id FROM abom_local_database
		WHERE repository = ? AND db_commit = ? AND source_filter = ? AND analysis_mode = ?`,
		d.Repository, d.Commit, d.SourceFilter, d.Mode).Scan(&id); err != nil {
		return 0, fmt.Errorf("reading back %s@%s: %w", d.Repository, d.Commit, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing %s@%s: %w", d.Repository, d.Commit, err)
	}
	return id, nil
}

// RetainedDatabaseAt reports the database held at one identity, if there is one.
//
// A miss is not an error: it means the corpus does not cover this question yet, which is a fact
// the caller reports rather than a failure.
func (s *Store) RetainedDatabaseAt(id DatabaseIdentity) (RetainedDatabase, bool, error) {
	d, err := scanRetainedDatabase(s.db.QueryRow(`
		SELECT `+retainedDatabaseColumns+` FROM abom_local_database
		WHERE repository = ? AND db_commit = ? AND source_filter = ? AND analysis_mode = ?`,
		id.Repository, id.Commit, id.SourceFilter, id.Mode))
	if errors.Is(err, sql.ErrNoRows) {
		return RetainedDatabase{}, false, nil
	}
	if err != nil {
		return RetainedDatabase{}, false, fmt.Errorf("reading %s@%s: %w",
			id.Repository, id.Commit, err)
	}
	return d, true, nil
}

// UseDatabase records that something read this database, and reports whether it was there.
//
// Recency is what eviction orders by, so a database serving a run is not the one dropped.
func (s *Store) UseDatabase(id DatabaseIdentity) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	seq, err := nextUseSeq(tx)
	if err != nil {
		return false, err
	}
	res, err := tx.Exec(`
		UPDATE abom_local_database SET last_used_at = ?, used_seq = ?
		WHERE repository = ? AND db_commit = ? AND source_filter = ? AND analysis_mode = ?`,
		time.Now().UTC().Format(time.RFC3339), seq,
		id.Repository, id.Commit, id.SourceFilter, id.Mode)
	if err != nil {
		return false, fmt.Errorf("using %s@%s: %w", id.Repository, id.Commit, err)
	}
	touched, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("using %s@%s: %w", id.Repository, id.Commit, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing the use of %s@%s: %w",
			id.Repository, id.Commit, err)
	}
	return touched > 0, nil
}

// RetainedDatabases lists everything the cache holds.
func (s *Store) RetainedDatabases() ([]RetainedDatabase, error) {
	rows, err := s.db.Query(`
		SELECT ` + retainedDatabaseColumns + ` FROM abom_local_database
		ORDER BY repository, db_commit, analysis_mode`)
	if err != nil {
		return nil, fmt.Errorf("listing retained databases: %w", err)
	}
	return collectRows(rows, func(rows *sql.Rows) (RetainedDatabase, error) {
		d, err := scanRetainedDatabase(rows)
		if err != nil {
			return RetainedDatabase{}, fmt.Errorf("scanning a retained database: %w", err)
		}
		return d, nil
	})
}

// RetainedDatabaseBytes is what the cache currently occupies.
func (s *Store) RetainedDatabaseBytes() (int64, error) {
	var total int64
	if err := s.db.QueryRow(
		`SELECT COALESCE(SUM(bytes), 0) FROM abom_local_database`).Scan(&total); err != nil {
		return 0, fmt.Errorf("measuring the database cache: %w", err)
	}
	return total, nil
}

// EvictDatabasesOver drops least-recently-used databases until the cache fits the budget, and
// returns every one it dropped.
//
// The rows go here and the directories go with the caller, which is the only order that leaves a
// recoverable state: an unreferenced directory can be reclaimed, whereas a row naming a directory
// somebody already deleted is a cache hit that cannot be served.
//
// It reports what it took rather than how much, because a run that cannot analyze a repository has
// to be able to say the database was evicted instead of reporting a repository with no results.
// Nothing here knows what is in use; holding a database against eviction belongs to the caller
// that is reading it.
func (s *Store) EvictDatabasesOver(budget int64) ([]RetainedDatabase, error) {
	dropped := []RetainedDatabase{}
	total, err := s.RetainedDatabaseBytes()
	if err != nil {
		return nil, err
	}
	if total <= budget {
		return dropped, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT ` + retainedDatabaseColumns + ` FROM abom_local_database ORDER BY used_seq, row_id`)
	if err != nil {
		return nil, fmt.Errorf("choosing databases to evict: %w", err)
	}
	// Collected before anything is deleted: the cursor and the delete share one connection.
	held := []RetainedDatabase{}
	for rows.Next() {
		d, err := scanRetainedDatabase(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning a database to evict: %w", err)
		}
		held = append(held, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("choosing databases to evict: %w", err)
	}

	for _, d := range held {
		if total <= budget {
			break
		}
		if _, err := tx.Exec(
			`DELETE FROM abom_local_database WHERE row_id = ?`, d.RowID); err != nil {
			return nil, fmt.Errorf("evicting %s@%s: %w", d.Repository, d.Commit, err)
		}
		total -= d.Bytes
		dropped = append(dropped, d)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing an eviction: %w", err)
	}
	return dropped, nil
}

// nextUseSeq is the counter eviction orders by. See the column's comment for why the timestamp
// beside it cannot serve.
func nextUseSeq(tx *sql.Tx) (int64, error) {
	var seq int64
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(used_seq), 0) + 1 FROM abom_local_database`).Scan(&seq); err != nil {
		return 0, fmt.Errorf("reading the database cache use counter: %w", err)
	}
	return seq, nil
}
