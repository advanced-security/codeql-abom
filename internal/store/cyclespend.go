package store

import (
	"fmt"
)

// cycleSpendDDL records what one crawl cycle cost and what it bought.
//
// Its own table rather than `abom_observation`: that one requires a `query_run_id` and is keyed
// by node, edge or repository, because it holds what a CodeQL query said about a subject. A cycle
// is not a subject and no query produced it.
//
// Append-only, and keyed by a surrogate rather than by cycle number: a restarted process counts
// from 1 again, so the number is not an identity and using it would let a second run overwrite
// the first run's history.
const cycleSpendDDL = `
CREATE TABLE IF NOT EXISTS abom_cycle_spend (
  row_id       INTEGER PRIMARY KEY,
  cycle        INTEGER NOT NULL,
  started_at   TEXT NOT NULL,
  recorded_at  TEXT NOT NULL,
  millis       INTEGER NOT NULL DEFAULT 0,
  -- What was spent.
  fetches      INTEGER NOT NULL DEFAULT 0,
  builds       INTEGER NOT NULL DEFAULT 0,
  evaluations  INTEGER NOT NULL DEFAULT 0,
  probes       INTEGER NOT NULL DEFAULT 0,
  -- What it bought.
  analyzed     INTEGER NOT NULL DEFAULT 0,
  skipped      INTEGER NOT NULL DEFAULT 0,
  settled      INTEGER NOT NULL DEFAULT 0,
  discovered   INTEGER NOT NULL DEFAULT 0,
  drifted      INTEGER NOT NULL DEFAULT 0,
  -- Where the objective stood. NULL means nothing was outstanding, which is a different
  -- statement from working at depth 0.
  shallowest_incomplete INTEGER
);
CREATE INDEX IF NOT EXISTS idx_cycle_spend_time ON abom_cycle_spend(recorded_at DESC);`

// CycleSpend is one cycle's cost and yield.
type CycleSpend struct {
	Cycle     int    `json:"cycle"`
	StartedAt string `json:"startedAt"`
	Millis    int64  `json:"millis"`

	Fetches     int `json:"fetches"`
	Builds      int `json:"builds"`
	Evaluations int `json:"evaluations"`
	Probes      int `json:"probes"`

	Analyzed   int `json:"analyzed"`
	Skipped    int `json:"skipped"`
	Settled    int `json:"settled"`
	Discovered int `json:"discovered"`
	Drifted    int `json:"drifted"`

	// ShallowestIncomplete is the depth the crawl was working on. HasOutstanding tells a complete
	// closure from work at depth 0.
	ShallowestIncomplete int  `json:"shallowestIncomplete"`
	HasOutstanding       bool `json:"hasOutstanding"`
}

// CycleTotal sums recorded cycles, so "under budget" has something to check against.
type CycleTotal struct {
	Cycles      int   `json:"cycles"`
	Millis      int64 `json:"millis"`
	Fetches     int   `json:"fetches"`
	Builds      int   `json:"builds"`
	Evaluations int   `json:"evaluations"`
	Probes      int   `json:"probes"`
	Analyzed    int   `json:"analyzed"`
	Discovered  int   `json:"discovered"`
}

// RecordCycle appends what a cycle spent.
func (s *Store) RecordCycle(c CycleSpend) error {
	var shallowest any
	if c.HasOutstanding {
		shallowest = c.ShallowestIncomplete
	}
	_, err := s.db.Exec(`
		INSERT INTO abom_cycle_spend
			(cycle, started_at, recorded_at, millis, fetches, builds, evaluations, probes,
			 analyzed, skipped, settled, discovered, drifted, shallowest_incomplete)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Cycle, c.StartedAt, now(), c.Millis, c.Fetches, c.Builds, c.Evaluations, c.Probes,
		c.Analyzed, c.Skipped, c.Settled, c.Discovered, c.Drifted, shallowest)
	if err != nil {
		return fmt.Errorf("recording cycle %d: %w", c.Cycle, err)
	}
	return nil
}

// RecentCycles reads the most recent cycles, newest first.
func (s *Store) RecentCycles(limit int) ([]CycleSpend, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT cycle, started_at, millis, fetches, builds, evaluations, probes,
		       analyzed, skipped, settled, discovered, drifted, shallowest_incomplete
		FROM abom_cycle_spend
		ORDER BY row_id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("reading recent cycles: %w", err)
	}
	defer rows.Close()

	out := []CycleSpend{}
	for rows.Next() {
		var c CycleSpend
		var shallowest *int
		if err := rows.Scan(&c.Cycle, &c.StartedAt, &c.Millis, &c.Fetches, &c.Builds,
			&c.Evaluations, &c.Probes, &c.Analyzed, &c.Skipped, &c.Settled, &c.Discovered,
			&c.Drifted, &shallowest); err != nil {
			return nil, fmt.Errorf("scanning a cycle: %w", err)
		}
		if shallowest != nil {
			c.ShallowestIncomplete, c.HasOutstanding = *shallowest, true
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CycleTotals sums every recorded cycle.
//
// A database that has never run one reports zero cycles, which a reader must not confuse with a
// crawl that ran and cost nothing.
func (s *Store) CycleTotals() (CycleTotal, error) {
	var t CycleTotal
	err := s.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(millis), 0), COALESCE(SUM(fetches), 0),
		       COALESCE(SUM(builds), 0), COALESCE(SUM(evaluations), 0), COALESCE(SUM(probes), 0),
		       COALESCE(SUM(analyzed), 0), COALESCE(SUM(discovered), 0)
		FROM abom_cycle_spend`).Scan(&t.Cycles, &t.Millis, &t.Fetches, &t.Builds, &t.Evaluations,
		&t.Probes, &t.Analyzed, &t.Discovered)
	if err != nil {
		return t, fmt.Errorf("totalling cycle spend: %w", err)
	}
	return t, nil
}
