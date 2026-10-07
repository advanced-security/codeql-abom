package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// reachMarkDDL records what stored reach was derived from, so the next derivation can name changes.
const reachMarkDDL = `
-- Which derivation stored reach answers for; no row means the next derivation is a rebuild.
CREATE TABLE IF NOT EXISTS abom_reach_mark (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  -- Its walk, depth bound and build: a mark from any other says nothing about this one.
  version   TEXT NOT NULL,
  marked_at TEXT NOT NULL
);

-- What it read at each node as digests; an absent node had no live reference and no advisory.
CREATE TABLE IF NOT EXISTS abom_reach_input (
  node_row_id INTEGER PRIMARY KEY,
  walk        TEXT NOT NULL DEFAULT '',
  advisories  TEXT NOT NULL DEFAULT ''
);
`

// ReachInput is what a derivation read at one node; zero is unreferenced and uncovered.
type ReachInput struct {
	Walk       string
	Advisories string
}

// ReachMark is what the stored reach was derived from.
type ReachMark struct {
	Version string
	Inputs  map[int64]ReachInput
}

// ReadReachMark reads the mark, reporting false when there is none.
func (s *Store) ReadReachMark() (ReachMark, bool, error) {
	out := ReachMark{Inputs: map[int64]ReachInput{}}
	err := s.db.QueryRow(`SELECT version FROM abom_reach_mark WHERE singleton = 1`).Scan(&out.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, fmt.Errorf("reading the reach mark: %w", err)
	}
	rows, err := s.db.Query(`SELECT node_row_id, walk, advisories FROM abom_reach_input`)
	if err != nil {
		return out, false, fmt.Errorf("reading what reach was derived from: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var node int64
		var in ReachInput
		if err := rows.Scan(&node, &in.Walk, &in.Advisories); err != nil {
			return out, false, fmt.Errorf("scanning what reach was derived from: %w", err)
		}
		out.Inputs[node] = in
	}
	return out, true, rows.Err()
}

// ClearReachMark makes the next derivation rebuild, in a transaction rewriting reach unmarked.
func ClearReachMark(tx *sql.Tx) error {
	for _, table := range []string{"abom_reach_mark", "abom_reach_input"} {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return fmt.Errorf("clearing %s: %w", table, err)
		}
	}
	return nil
}

// MarkReach records what tx's derivation read: every input when whole, else those named.
func MarkReach(tx *sql.Tx, version string, inputs map[int64]ReachInput, whole bool) error {
	if whole {
		if _, err := tx.Exec(`DELETE FROM abom_reach_input`); err != nil {
			return fmt.Errorf("clearing what reach was derived from: %w", err)
		}
	}
	put, err := tx.Prepare(`INSERT INTO abom_reach_input (node_row_id, walk, advisories)
		VALUES (?, ?, ?)
		ON CONFLICT(node_row_id) DO UPDATE SET walk = excluded.walk, advisories = excluded.advisories`)
	if err != nil {
		return fmt.Errorf("preparing the reach input upsert: %w", err)
	}
	defer put.Close()
	drop, err := tx.Prepare(`DELETE FROM abom_reach_input WHERE node_row_id = ?`)
	if err != nil {
		return fmt.Errorf("preparing the reach input delete: %w", err)
	}
	defer drop.Close()
	for node, in := range inputs {
		if in == (ReachInput{}) {
			if _, err := drop.Exec(node); err != nil {
				return fmt.Errorf("forgetting what node %d read: %w", node, err)
			}
			continue
		}
		if _, err := put.Exec(node, in.Walk, in.Advisories); err != nil {
			return fmt.Errorf("recording what node %d read: %w", node, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO abom_reach_mark (singleton, version, marked_at) VALUES (1, ?, ?)
		ON CONFLICT(singleton) DO UPDATE SET version = excluded.version, marked_at = excluded.marked_at`,
		version, now()); err != nil {
		return fmt.Errorf("recording the reach mark: %w", err)
	}
	return nil
}
