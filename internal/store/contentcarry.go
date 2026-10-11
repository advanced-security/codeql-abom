package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// HasCommitContentLabeled reports whether a commit of a repository other than except has content
// recorded under a label, the filter and scheme a digest begins with.
//
// A caller that can compute a digest only in one scheme asks this before paying to compute one:
// with nothing else recorded in that scheme, the digest it would compute can match nothing.
func (s *Store) HasCommitContentLabeled(repository, label, except string) (bool, error) {
	if repository == "" || label == "" {
		return false, nil
	}
	var found bool
	err := s.db.QueryRow(`
		SELECT EXISTS(
		  SELECT 1 FROM abom_commit_content
		   WHERE repository = ?1 AND commit_sha <> ?3
		     AND substr(indexed_digest, 1, length(?2)) = ?2)`,
		repository, label, except).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("looking for content of %s labeled %s: %w", repository, label, err)
	}
	return found, nil
}

// CarryLockReading gives a commit the lock reading of another commit of the same repository whose
// indexed content is the digest given, reporting whether there was a reading to carry.
//
// Only sound where the digest covers everything the lock reader reads, which only the caller can
// say. The reading carried is one this reader need not repeat, so it is what reading the lock at
// this commit would record, in the lock's own order. One transaction, for the reason
// RecordLockReading gives.
func (s *Store) CarryLockReading(repository, commit, digest, reader string) (bool, error) {
	if repository == "" || commit == "" || digest == "" {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("carrying a lock reading to %s at %s: %w", repository, commit, err)
	}
	defer func() { _ = tx.Rollback() }()

	var from string
	err = tx.QueryRow(`
		SELECT c.commit_sha
		  FROM abom_commit_content c
		  JOIN abom_lock_coverage l
		    ON l.repository = c.repository AND l.commit_sha = c.commit_sha AND l.workflow_path = ''
		 WHERE c.repository = ? AND c.indexed_digest = ? AND c.commit_sha <> ?
		   AND `+readByCurrentReader+`
		 ORDER BY l.seen_at DESC, c.commit_sha
		 LIMIT 1`, repository, digest, commit, reader).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("finding a lock reading identical to %s at %s: %w", repository, commit, err)
	}

	for _, table := range []string{
		"abom_lock_coverage", "abom_lock_dependency", "abom_lock_use", "abom_lock_workflow_pin",
	} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE repository = ? AND commit_sha = ?`,
			repository, commit); err != nil {
			return false, fmt.Errorf("clearing %s for %s at %s: %w", table, repository, commit, err)
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO abom_lock_coverage
		    (repository, commit_sha, workflow_path, coverage, schema_version, lock_digest, reason,
		     seen_at, graph_recorded, reader)
		SELECT repository, ?, workflow_path, coverage, schema_version, lock_digest, reason,
		       ?, graph_recorded, reader
		  FROM abom_lock_coverage WHERE repository = ? AND commit_sha = ?`,
		commit, now(), repository, from); err != nil {
		return false, fmt.Errorf("carrying lock coverage from %s to %s in %s: %w",
			from, commit, repository, err)
	}
	for _, graph := range []struct{ table, columns string }{
		{"abom_lock_dependency", "pin_key, owner, repo, ref, resolved_from, digest_algorithm, " +
			"digest, owner_id, repo_id"},
		{"abom_lock_use", "parent_pin, child_pin"},
		{"abom_lock_workflow_pin", "workflow_path, pin_key"},
	} {
		if _, err := tx.Exec(`
			INSERT INTO `+graph.table+` (repository, commit_sha, `+graph.columns+`)
			SELECT repository, ?, `+graph.columns+`
			  FROM `+graph.table+` WHERE repository = ? AND commit_sha = ?
			 ORDER BY rowid`,
			commit, repository, from); err != nil {
			return false, fmt.Errorf("carrying %s from %s to %s in %s: %w",
				graph.table, from, commit, repository, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("carrying a lock reading to %s at %s: %w", repository, commit, err)
	}
	return true, nil
}
