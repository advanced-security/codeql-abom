package store

import (
	"database/sql"
	"fmt"
)

// commitContentDDL records what the extractor would read at one commit.
//
// Keyed by repository and commit rather than by digest alone: the same content in a fork is a
// different repository's evidence, and crediting one repository's analysis to another is the
// conflation `origin` exists to prevent elsewhere.
const commitContentDDL = `
CREATE TABLE IF NOT EXISTS abom_commit_content (
  repository     TEXT NOT NULL,
  commit_sha     TEXT NOT NULL,
  -- Hash over the files the sparse filter fetches, which is what a database is built from.
  indexed_digest TEXT NOT NULL,
  seen_at        TEXT NOT NULL,
  PRIMARY KEY (repository, commit_sha)
);
-- The lookup runs the other way round: given a digest, which commits of this repository had it.
CREATE INDEX IF NOT EXISTS idx_commit_content_digest
  ON abom_commit_content(repository, indexed_digest);`

// RecordCommitContent notes what one commit's indexed content hashes to.
func (s *Store) RecordCommitContent(repository, commit, digest string) error {
	if repository == "" || commit == "" {
		return fmt.Errorf("store: RecordCommitContent needs a repository and a commit")
	}
	_, err := s.db.Exec(`
		INSERT INTO abom_commit_content (repository, commit_sha, indexed_digest, seen_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(repository, commit_sha) DO UPDATE SET
		    indexed_digest = excluded.indexed_digest, seen_at = excluded.seen_at`,
		repository, commit, digest, now())
	if err != nil {
		return fmt.Errorf("recording content of %s at %s: %w", repository, commit, err)
	}
	return nil
}

// CommitContentDigest reports what a commit's indexed content hashed to, if it has been seen.
//
// The pre-check runs before any fetch, so a digest it has not already recorded is one it cannot
// compute — absent is reported as absent rather than as empty.
func (s *Store) CommitContentDigest(repository, commit string) (string, bool, error) {
	var digest string
	err := s.db.QueryRow(
		`SELECT indexed_digest FROM abom_commit_content WHERE repository = ? AND commit_sha = ?`,
		repository, commit).Scan(&digest)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading content digest for %s: %w", repository, err)
	}
	return digest, digest != "", nil
}

// QueryRunExistsForContent reports whether this query has run, at this identity, against a
// database built from exactly this content — whatever commit that content was fetched at.
//
// Every part of the run key still has to match except the commit, which content stands in for.
// CodeQL is deterministic, so a database built from byte-identical sources cannot answer
// differently; that is the same reasoning `QueryRunExists` uses, with the commit replaced by the
// thing the commit was standing for. An action's release moves a tag over a rebuilt `dist/` and
// touches nothing the extractor reads, which is the case this exists for.
//
// Nothing is written on this path. The run stays recorded against the commit it actually ran at,
// and the findings stay attributed to it, because recording a run that did not happen is the one
// way this could turn into a claim ABOM has not earned.
func (s *Store) QueryRunExistsForContent(q QueryRun, digest string) (bool, error) {
	if q.QueryID == "" || q.Repository == "" {
		return false, fmt.Errorf("store: QueryRun needs QueryID and Repository")
	}
	// An empty digest is a caller that could not read the tree. Matching on it would make every
	// unreadable checkout equivalent to every other.
	if digest == "" {
		return false, nil
	}

	var one int
	err := s.db.QueryRow(`
		SELECT 1
		FROM abom_query_run r
		JOIN abom_commit_content c
		  ON c.repository = r.repository AND c.commit_sha = r.db_commit
		WHERE r.repository = ?
		  AND r.query_id = ?
		  AND COALESCE(r.query_version,'') = ?
		  AND COALESCE(r.pack,'') = ?
		  AND COALESCE(r.pack_version,'') = ?
		  AND COALESCE(r.pack_digest,'') = ?
		  AND COALESCE(r.cli_version,'') = ?
		  AND c.indexed_digest = ?
		LIMIT 1`,
		q.Repository, q.QueryID, q.QueryVersion, q.Pack, q.PackVersion, q.PackDigest,
		q.CLIVersion, digest).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("looking up an equivalent run for %s: %w", q.QueryID, err)
	}
	return true, nil
}
