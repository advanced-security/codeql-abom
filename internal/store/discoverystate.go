package store

import (
	"database/sql"
	"errors"
	"time"
)

// discoveryStateDDL remembers whether the operator wants discovery running.
//
// One row, because this is a single fact about the installation rather than a log of it. The
// timestamp is here so an operator can tell a choice they made from one a restart inherited.
const discoveryStateDDL = `
CREATE TABLE IF NOT EXISTS abom_discovery_state (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  running    INTEGER NOT NULL,
  changed_at TEXT NOT NULL
);`

// SetDiscoveryIntent records whether discovery should be running.
//
// Intent rather than state: it says what the operator asked for, not what the loop is doing now.
// A crawl that is between cycles, or blocked by a preflight, is still intended to run.
func (s *Store) SetDiscoveryIntent(running bool) error {
	_, err := s.db.Exec(`
INSERT INTO abom_discovery_state (id, running, changed_at) VALUES (1, ?, ?)
ON CONFLICT(id) DO UPDATE SET running = excluded.running, changed_at = excluded.changed_at`,
		running, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// DiscoveryIntent reports what the operator last asked for, and whether they ever asked.
//
// Never-asked is its own answer: a database nobody has started is not one somebody paused, and
// starting a crawl on an installation that never requested one would spend somebody's rate limit
// on a decision they did not make.
func (s *Store) DiscoveryIntent() (running, known bool, err error) {
	var v int
	switch err := s.db.QueryRow(`SELECT running FROM abom_discovery_state WHERE id = 1`).Scan(&v); {
	case errors.Is(err, sql.ErrNoRows):
		return false, false, nil
	case err != nil:
		return false, false, err
	}
	return v != 0, true, nil
}
