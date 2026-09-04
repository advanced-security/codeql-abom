package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Ingest status values.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// Unit identifies one indivisible piece of ingestion work: one query evaluated against one
// repository's database at one commit.
//
// Splitting work this way is what keeps a 20,000-repository run restartable. Nothing is
// processed as a single batch, and a crash costs at most one unit.
type Unit struct {
	Repository       string
	DBCommit         string
	QueryID          string
	QueryVersion     string
	ExtractorVersion string
}

// Key is the stable identity of the unit.
//
// ExtractorVersion participates so that changing how results are interpreted re-runs the work
// even when the repository and query are untouched.
func (u Unit) Key() string {
	h := sha256.New()
	for _, part := range []string{
		u.Repository, u.DBCommit, u.QueryID, u.QueryVersion, u.ExtractorVersion,
	} {
		// Length-prefixing prevents distinct field splits from colliding on one digest.
		fmt.Fprintf(h, "%d:%s\x00", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ShouldProcess reports whether the unit still needs work.
//
// Units already recorded as done are skipped, which is what makes repeated scans cheap and
// lets the graph accumulate over time instead of being rebuilt.
func (s *Store) ShouldProcess(u Unit) (bool, error) {
	var status string
	err := s.db.QueryRow(
		`SELECT status FROM abom_ingest_ledger WHERE unit_key = ?`, u.Key()).Scan(&status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil
		}
		return false, fmt.Errorf("reading ledger: %w", err)
	}
	return status != StatusDone, nil
}

// BeginUnit records that work has started, replacing any previous failed attempt.
func (s *Store) BeginUnit(u Unit) error {
	_, err := s.db.Exec(`
		INSERT INTO abom_ingest_ledger
			(unit_key, repository, db_commit, query_id, query_version, extractor_version,
			 status, rows_ingested, started_at, finished_at, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, NULL, NULL)
		ON CONFLICT(unit_key) DO UPDATE SET
			status     = excluded.status,
			started_at = excluded.started_at,
			finished_at = NULL,
			error      = NULL`,
		u.Key(), u.Repository, nullable(u.DBCommit), u.QueryID, nullable(u.QueryVersion),
		nullable(u.ExtractorVersion), StatusRunning, now())
	if err != nil {
		return fmt.Errorf("starting unit %s: %w", u.Key(), err)
	}
	return nil
}

// CompleteUnit marks the unit done and records how many rows it contributed.
func (s *Store) CompleteUnit(u Unit, rows int) error {
	_, err := s.db.Exec(`
		UPDATE abom_ingest_ledger
		SET status = ?, rows_ingested = ?, finished_at = ?, error = NULL
		WHERE unit_key = ?`, StatusDone, rows, now(), u.Key())
	if err != nil {
		return fmt.Errorf("completing unit %s: %w", u.Key(), err)
	}
	return nil
}

// FailUnit records a failure so the unit is retried on the next run.
func (s *Store) FailUnit(u Unit, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	if _, err := s.db.Exec(`
		UPDATE abom_ingest_ledger
		SET status = ?, finished_at = ?, error = ?
		WHERE unit_key = ?`, StatusFailed, now(), nullable(msg), u.Key()); err != nil {
		return fmt.Errorf("failing unit %s: %w", u.Key(), err)
	}
	return nil
}

// LedgerCounts summarizes progress by status, for resumable-run reporting.
func (s *Store) LedgerCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM abom_ingest_ledger GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("counting ledger: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scanning ledger counts: %w", err)
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
