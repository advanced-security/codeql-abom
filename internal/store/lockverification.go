package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// lockVerificationDDL keeps each run of GitHub's lock verification that somebody asked for.
//
// Append-only: a verification is evidence about one commit at one time, and a later one does not
// make an earlier one untrue. valid is NULL when the tool gave no report, which is not false.
const lockVerificationDDL = `
CREATE TABLE IF NOT EXISTS abom_lock_verification (
  row_id           INTEGER PRIMARY KEY,
  repository       TEXT NOT NULL COLLATE NOCASE,
  commit_sha       TEXT NOT NULL,
  mode             TEXT NOT NULL,
  outcome          TEXT NOT NULL,
  tool_version     TEXT NOT NULL DEFAULT '',
  lockfile_version TEXT NOT NULL DEFAULT '',
  valid            INTEGER,
  findings         TEXT NOT NULL DEFAULT '[]',
  error            TEXT NOT NULL DEFAULT '',
  requested_by     TEXT NOT NULL DEFAULT '',
  verified_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lock_verification_commit
  ON abom_lock_verification (repository, commit_sha, mode, verified_at);`

// LockVerification is one run of the tool, as it was recorded.
type LockVerification struct {
	ID              int64                     `json:"id"`
	Repository      string                    `json:"repository"`
	Commit          string                    `json:"commit"`
	Mode            string                    `json:"mode"`
	Outcome         string                    `json:"outcome"`
	ToolVersion     string                    `json:"toolVersion"`
	LockfileVersion string                    `json:"lockfileVersion"`
	Valid           *bool                     `json:"valid"`
	Findings        []LockVerificationFinding `json:"findings"`
	Error           string                    `json:"error,omitempty"`
	RequestedBy     string                    `json:"requestedBy,omitempty"`
	VerifiedAt      time.Time                 `json:"verifiedAt"`
}

// LockVerificationFinding is one diagnostic the tool reported, verbatim, and whether this build
// knows its category.
type LockVerificationFinding struct {
	Workflow   string `json:"workflow"`
	Category   string `json:"category"`
	Severity   string `json:"severity"`
	Confidence string `json:"confidence,omitempty"`
	Dependency string `json:"dependency,omitempty"`
	RequiredBy string `json:"requiredBy,omitempty"`
	Detail     string `json:"detail"`
	Known      bool   `json:"known"`
}

// RecordLockVerification keeps one run.
func (s *Store) RecordLockVerification(v LockVerification) (int64, error) {
	if v.Repository == "" || v.Commit == "" || v.Mode == "" || v.Outcome == "" {
		return 0, fmt.Errorf("store: a lock verification needs a repository, commit, mode and outcome")
	}
	findings := v.Findings
	if findings == nil {
		findings = []LockVerificationFinding{}
	}
	body, err := json.Marshal(findings)
	if err != nil {
		return 0, fmt.Errorf("encoding lock verification findings: %w", err)
	}
	var valid any
	if v.Valid != nil {
		valid = *v.Valid
	}
	at := v.VerifiedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var id int64
	err = s.db.QueryRow(`
		INSERT INTO abom_lock_verification
		  (repository, commit_sha, mode, outcome, tool_version, lockfile_version, valid, findings,
		   error, requested_by, verified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING row_id`,
		v.Repository, strings.ToLower(v.Commit), v.Mode, v.Outcome, v.ToolVersion,
		v.LockfileVersion, valid, string(body), v.Error, v.RequestedBy,
		at.UTC().Format(time.RFC3339Nano)).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("recording a lock verification of %s: %w", v.Repository, err)
	}
	return id, nil
}

const lockVerificationColumns = `row_id, repository, commit_sha, mode, outcome, tool_version,
	lockfile_version, valid, findings, error, requested_by, verified_at`

func scanLockVerification(row interface{ Scan(...any) error }) (LockVerification, error) {
	var v LockVerification
	var valid sql.NullBool
	var findings, at string
	if err := row.Scan(&v.ID, &v.Repository, &v.Commit, &v.Mode, &v.Outcome, &v.ToolVersion,
		&v.LockfileVersion, &valid, &findings, &v.Error, &v.RequestedBy, &at); err != nil {
		return v, err
	}
	if valid.Valid {
		v.Valid = &valid.Bool
	}
	v.Findings = []LockVerificationFinding{}
	if err := json.Unmarshal([]byte(findings), &v.Findings); err != nil {
		return v, fmt.Errorf("reading the findings of lock verification %d: %w", v.ID, err)
	}
	v.VerifiedAt, _ = time.Parse(time.RFC3339Nano, at)
	return v, nil
}

// LockVerifications lists one repository's runs, newest first.
func (s *Store) LockVerifications(repository string, limit int) ([]LockVerification, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT `+lockVerificationColumns+`
		FROM abom_lock_verification WHERE repository = ?
		ORDER BY verified_at DESC, row_id DESC LIMIT ?`, repository, limit)
	if err != nil {
		return nil, fmt.Errorf("reading lock verifications of %s: %w", repository, err)
	}
	return collectRows(rows, func(rows *sql.Rows) (LockVerification, error) {
		return scanLockVerification(rows)
	})
}

// LastLockVerification is the newest run of one mode at one commit, if there is one.
func (s *Store) LastLockVerification(repository, commit, mode string) (LockVerification, bool, error) {
	v, err := scanLockVerification(s.db.QueryRow(`SELECT `+lockVerificationColumns+`
		FROM abom_lock_verification
		WHERE repository = ? AND commit_sha = ? AND mode = ?
		ORDER BY verified_at DESC, row_id DESC LIMIT 1`, repository, strings.ToLower(commit), mode))
	if err == sql.ErrNoRows {
		return LockVerification{}, false, nil
	}
	if err != nil {
		return LockVerification{}, false, fmt.Errorf("reading the last lock verification of %s: %w", repository, err)
	}
	return v, true, nil
}

// LockVerificationsSince counts the runs of one mode since a time, which is what a budget spends.
func (s *Store) LockVerificationsSince(mode string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_lock_verification
		WHERE mode = ? AND verified_at >= ?`, mode, since.UTC().Format(time.RFC3339Nano)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting lock verifications: %w", err)
	}
	return n, nil
}

// CurrentLockfileCommit is the commit a verification of this repository is about: one it
// currently resolves to at which a lockfile was read, readable or not. The newest reading wins
// where the repository resolves to several.
func (s *Store) CurrentLockfileCommit(repository string) (string, bool, error) {
	var commit string
	err := s.db.QueryRow(`
		WITH current_root(repository, commit_sha) AS (`+currentRoots+`)
		SELECT c.commit_sha FROM abom_lock_coverage c
		JOIN current_root r ON r.repository = LOWER(c.repository)
		                   AND r.commit_sha = LOWER(c.commit_sha)
		WHERE LOWER(c.repository) = LOWER(?) AND c.workflow_path = ''
		  AND c.coverage IN ('covered', 'unsupported')
		ORDER BY c.seen_at DESC LIMIT 1`, repository).Scan(&commit)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading the lockfile commit of %s: %w", repository, err)
	}
	return commit, true, nil
}
