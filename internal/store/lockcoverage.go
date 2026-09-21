package store

import (
	"fmt"

	"github.com/advanced-security/codeql-abom/internal/lockfile"
)

// lockCoverageDDL records what a repository's Actions lockfile said at one commit.
//
// Keyed by repository, commit and workflow path because coverage is per workflow: a lockfile can
// exist and say nothing about a given workflow, which is a different fact from the repository
// having no lockfile at all. The repository-level answer -- no lockfile, or one this consumer
// cannot read -- is the row whose workflow path is empty.
//
// Nothing here is a verdict. A recorded coverage state says what the lockfile claims, never that
// the selection was verified against GitHub or that the workflow is safe.
const lockCoverageDDL = `
CREATE TABLE IF NOT EXISTS abom_lock_coverage (
  repository    TEXT NOT NULL,
  commit_sha    TEXT NOT NULL,
  -- Empty for the repository-level state, so the key never contains NULL.
  workflow_path TEXT NOT NULL DEFAULT '',
  coverage      TEXT NOT NULL,
  -- The version as written, retained even when it is the reason the file is unreadable.
  schema_version TEXT NOT NULL DEFAULT '',
  -- sha256 over the original bytes, taken before parsing.
  lock_digest   TEXT NOT NULL DEFAULT '',
  reason        TEXT NOT NULL DEFAULT '',
  seen_at       TEXT NOT NULL,
  PRIMARY KEY (repository, commit_sha, workflow_path)
);
-- The measurement reads by state across the corpus, not by repository.
CREATE INDEX IF NOT EXISTS idx_lock_coverage_state
  ON abom_lock_coverage(coverage);`

// LockCoverage is one recorded coverage row.
type LockCoverage struct {
	WorkflowPath string
	Coverage     string
	Schema       string
	Digest       string
	Reason       string
}

// LockCoverageRows builds the rows for one repository from a survey of its tree.
//
// The file's schema, digest and reason ride on every row, so a reader that selects one workflow
// still knows which lockfile answered for it.
func LockCoverageRows(coverage, schema, digest, reason string, workflows []lockfile.WorkflowCoverage) []LockCoverage {
	rows := make([]LockCoverage, 0, len(workflows))
	for _, w := range workflows {
		row := LockCoverage{
			WorkflowPath: w.Path,
			Coverage:     string(w.Coverage),
			Schema:       schema,
			Digest:       digest,
		}
		// Only the repository-level row carries the reason; a workflow did not fail to parse.
		if w.Path == "" {
			row.Coverage, row.Reason = coverage, reason
		}
		rows = append(rows, row)
	}
	return rows
}

// RecordLockCoverage notes what the lockfile said for one repository at one commit.
//
// Replaces that commit's rows rather than adding to them: a commit's lockfile is one file with
// one answer, so a second reading of the same commit is a correction, not a second opinion.
func (s *Store) RecordLockCoverage(repository, commit string, rows []LockCoverage) error {
	if repository == "" || commit == "" {
		return fmt.Errorf("store: RecordLockCoverage needs a repository and a commit")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("recording lock coverage for %s: %w", repository, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`DELETE FROM abom_lock_coverage WHERE repository = ? AND commit_sha = ?`,
		repository, commit); err != nil {
		return fmt.Errorf("clearing lock coverage for %s at %s: %w", repository, commit, err)
	}
	stmt, err := tx.Prepare(`
		INSERT INTO abom_lock_coverage
		    (repository, commit_sha, workflow_path, coverage, schema_version, lock_digest,
		     reason, seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing the lock coverage insert: %w", err)
	}
	defer stmt.Close()

	seen := now()
	for _, row := range rows {
		if _, err := stmt.Exec(repository, commit, row.WorkflowPath, row.Coverage,
			row.Schema, row.Digest, row.Reason, seen); err != nil {
			return fmt.Errorf("recording lock coverage for %s at %s: %w", repository, commit, err)
		}
	}
	return tx.Commit()
}

// LockCoverageCounts reports how many rows hold each coverage state.
//
// A state with no rows is absent from the result rather than present as zero, because the caller
// cannot otherwise tell a state nothing reached from one this build does not produce.
func (s *Store) LockCoverageCounts() (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT coverage, COUNT(*) FROM abom_lock_coverage GROUP BY coverage`)
	if err != nil {
		return nil, fmt.Errorf("counting lock coverage: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, fmt.Errorf("reading lock coverage counts: %w", err)
		}
		counts[state] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading lock coverage counts: %w", err)
	}
	return counts, nil
}

// HasLockCoverage reports whether this repository's lockfile was read at this commit.
//
// Asked before a reference is settled without a checkout: a commit analyzed before this table
// existed has no rows, and settling it again would leave the lockfile unread indefinitely. Every
// reading writes at least the repository-level row, including "no lockfile here", so absence of
// rows means the question was never asked rather than answered with nothing.
func (s *Store) HasLockCoverage(repository, commit string) (bool, error) {
	if repository == "" || commit == "" {
		return false, nil
	}
	var found int
	err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM abom_lock_coverage WHERE repository = ? AND commit_sha = ?)`,
		repository, commit).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("reading lock coverage for %s at %s: %w", repository, commit, err)
	}
	return found == 1, nil
}
