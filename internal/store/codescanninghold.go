package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// DefaultSetupRun is the workflow path of default setup's runs, whose completion says code
// scanning has analyzed a commit.
const DefaultSetupRun = "dynamic/github-code-scanning/codeql"

// DefaultSetupReadsActions says whether the last reading of a repository's default setup found it
// configured with Actions among its languages, and whether any reading answered at all: a setup
// nobody read is unknown, never off.
func (s *Store) DefaultSetupReadsActions(repository string) (reads, known bool, err error) {
	err = s.db.QueryRow(`
		SELECT state = 'configured'
		       AND EXISTS (SELECT 1 FROM json_each(COALESCE(languages, '[]')) j WHERE j.value = 'actions')
		  FROM abom_setup_reading WHERE repository = ? AND unreadable = ''`, repository).Scan(&reads)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("reading whether default setup reads Actions in %s: %w", repository, err)
	}
	return reads, true, nil
}

// DefaultSetupRanAt says whether a completed run of default setup at commit is on record.
func (s *Store) DefaultSetupRanAt(repository, commit string) (bool, error) {
	var ran bool
	if err := s.db.QueryRow(`
		SELECT EXISTS (SELECT 1 FROM abom_workflow_run
		                WHERE LOWER(repository) = LOWER(?) AND workflow_path = ?
		                  AND LOWER(head_sha) = LOWER(?))`,
		repository, DefaultSetupRun, commit).Scan(&ran); err != nil {
		return false, fmt.Errorf("reading whether default setup ran at %s@%s: %w", repository, commit, err)
	}
	return ran, nil
}
