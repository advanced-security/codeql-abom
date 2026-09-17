package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// RefResolution is where a tag pointed, and when.
//
// GitHub generates Dependabot alerts for actions "that use semantic versioning, not SHA
// versioning", so a reference pinned to a commit is invisible to remediation until something can
// name the version that commit was released as. Measured on the reference corpus, 10,881 of
// 31,871 live edges are pinned to a commit.
//
// The mapping is mostly a byproduct the crawl already produces and discards: resolving
// `actions/checkout@v4.2.2` to a commit is exactly a tag-to-commit measurement, and 888 of the
// 1,333 distinct pinned commits are recoverable from the queue with no request to GitHub at all.
type RefResolution struct {
	Owner     string
	Repo      string
	Tag       string
	CommitSHA string
	// Source separates what was measured from what was asserted. See the constants.
	Source string
	// ResolvedAt dates the reading, because a major-version tag moves. Defaults to now.
	ResolvedAt string
}

// Where a resolution came from. Only the measured ones may name a version for submission: a
// trailing `# v4.2.2` comment is what somebody intended, and a submission built on it would put a
// version into a monitored organization's dependency graph that nothing ever confirmed.
const (
	// ResolutionCrawl is a tag this tool watched resolve to a commit while analyzing it.
	ResolutionCrawl = "crawl"
	// ResolutionTags is a tag read back from GitHub's tag listing for the repository.
	ResolutionTags = "tags-api"
	// ResolutionComment is the version written in a trailing YAML comment. A claim, never a fact.
	ResolutionComment = "comment"
)

const refResolutionDDL = `
CREATE TABLE IF NOT EXISTS abom_ref_resolution (
  row_id      INTEGER PRIMARY KEY,
  owner       TEXT NOT NULL,
  repo        TEXT NOT NULL,
  tag         TEXT NOT NULL,
  commit_sha  TEXT NOT NULL,
  source      TEXT NOT NULL,
  resolved_at TEXT NOT NULL,
  UNIQUE(owner, repo, tag, commit_sha)
);`

// Separate because the reverse lookup is the hot path and indexes are built after bulk loading.
const refResolutionIndexDDL = `
CREATE INDEX IF NOT EXISTS abom_ref_resolution_by_commit
  ON abom_ref_resolution(owner, repo, commit_sha);`

// RecordRefResolution stores one reading, keeping the stronger source when both arrive.
//
// The pair rather than the tag is unique: a major-version tag points somewhere new after every
// release, and dropping the previous reading would leave a purl submitted last month unexplainable.
func (s *Store) RecordRefResolution(r RefResolution) error {
	if r.Owner == "" || r.Repo == "" || r.Tag == "" || r.CommitSHA == "" {
		return fmt.Errorf("a resolution needs an owner, repo, tag and commit")
	}
	if r.ResolvedAt == "" {
		r.ResolvedAt = time.Now().UTC().Format(time.RFC3339)
	}
	// A claim never displaces a measurement, and a measurement always promotes a claim, whichever
	// order they arrive in.
	_, err := s.db.Exec(`
		INSERT INTO abom_ref_resolution (owner, repo, tag, commit_sha, source, resolved_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner, repo, tag, commit_sha) DO UPDATE SET
		  source      = CASE WHEN excluded.source IN ('crawl','tags-api')
		                     THEN excluded.source ELSE abom_ref_resolution.source END,
		  resolved_at = CASE WHEN excluded.source IN ('crawl','tags-api')
		                     THEN excluded.resolved_at ELSE abom_ref_resolution.resolved_at END`,
		r.Owner, r.Repo, r.Tag, strings.ToLower(r.CommitSHA), r.Source, r.ResolvedAt)
	if err != nil {
		return fmt.Errorf("recording %s/%s@%s: %w", r.Owner, r.Repo, r.Tag, err)
	}
	return nil
}

// SemverForSHA names the version a commit was released as, for a purl that Dependabot can match.
//
// Measured sources only. The most specific tag wins, because `4.2.2` is a version and `4.%2A.%2A`
// is a range: a commit carrying v4, v4.2 and v4.2.2 should be submitted as the last of those.
func (s *Store) SemverForSHA(owner, repo, sha string) (string, bool, error) {
	var tag string
	err := s.db.QueryRow(`
		SELECT tag FROM abom_ref_resolution
		 WHERE LOWER(owner) = LOWER(?) AND LOWER(repo) = LOWER(?)
		   AND commit_sha = LOWER(?) AND source IN ('crawl','tags-api')
		 ORDER BY LENGTH(tag) - LENGTH(REPLACE(tag, '.', '')) DESC, LENGTH(tag) DESC, tag DESC
		 LIMIT 1`, owner, repo, sha).Scan(&tag)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("resolving a version for %s/%s@%s: %w", owner, repo, sha, err)
	}
	return tag, true, nil
}

// SHAForTag reports where a tag points now, which is the newest reading of it.
func (s *Store) SHAForTag(owner, repo, tag string) (string, bool, error) {
	var sha string
	err := s.db.QueryRow(`
		SELECT commit_sha FROM abom_ref_resolution
		 WHERE LOWER(owner) = LOWER(?) AND LOWER(repo) = LOWER(?) AND tag = ?
		   AND source IN ('crawl','tags-api')
		 ORDER BY resolved_at DESC, row_id DESC
		 LIMIT 1`, owner, repo, tag).Scan(&sha)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("resolving a commit for %s/%s@%s: %w", owner, repo, tag, err)
	}
	return sha, true, nil
}

// BackfillRefResolutions recovers what the crawl already resolved and never wrote down.
//
// Only rows whose ref reads as a version: a branch resolves to a commit too, and recording that
// as a version would name something no advisory range can be compared against.
func (s *Store) BackfillRefResolutions() (int, error) {
	res, err := s.db.Exec(`
		INSERT OR IGNORE INTO abom_ref_resolution
			(owner, repo, tag, commit_sha, source, resolved_at)
		SELECT f.owner, f.repo, f.ref, LOWER(f.resolved_sha), ?,
		       COALESCE(NULLIF(f.analyzed_at, ''), ?)
		  FROM abom_frontier f
		 WHERE f.owner IS NOT NULL AND f.repo IS NOT NULL
		   AND f.ref <> '' AND f.resolved_sha IS NOT NULL AND f.resolved_sha <> ''
		   AND (f.ref GLOB 'v[0-9]*' OR f.ref GLOB '[0-9]*')`,
		ResolutionCrawl, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("backfilling resolutions from the queue: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting backfilled resolutions: %w", err)
	}
	return int(n), nil
}
