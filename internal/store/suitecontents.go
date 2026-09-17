package store

import (
	"fmt"
	"time"
)

// SuiteQuery is one query a suite resolved to, and the pack that supplied it.
//
// The pack is the level that carries a version anybody can act on: a suite has no version of its
// own, being a program over packs rather than a list.
type SuiteQuery struct {
	Pack        string
	PackVersion string
	// PackDigest is the content hash of the pack, which is what actually decides whether the
	// query changed: a pack version pinned to the CodeQL CLI does not move when one is edited.
	PackDigest string
	QueryID    string
	Kind       string
	ResolvedAt string
}

// RecordSuiteContents replaces what a suite is known to resolve to.
//
// Replaced rather than accumulated: a suite is re-resolved every round, and a query dropped
// upstream must stop being listed rather than linger as something the crawl still runs. This is
// a derived cache, so it is not covered by the append-only rule that governs the graph tables.
func (s *Store) RecordSuiteContents(suite string, queries []SuiteQuery) error {
	if suite == "" {
		return fmt.Errorf("store: a suite reference is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM abom_suite_query WHERE suite = ?`, suite); err != nil {
		return fmt.Errorf("clearing what %s held: %w", suite, err)
	}
	for _, q := range queries {
		if _, err := tx.Exec(`
			INSERT INTO abom_suite_query
				(suite, pack, pack_version, pack_digest, query_id, kind, resolved_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			suite, q.Pack, q.PackVersion, q.PackDigest, q.QueryID, q.Kind, now); err != nil {
			return fmt.Errorf("recording %s in %s: %w", q.QueryID, suite, err)
		}
	}
	return tx.Commit()
}

// SuiteContents reads what each suite last resolved to.
//
// A suite nobody has resolved is absent rather than present and empty, because "never measured"
// and "measured as nothing" are different facts and only one of them is a count.
func (s *Store) SuiteContents() (map[string][]SuiteQuery, error) {
	rows, err := s.db.Query(`
		SELECT suite, pack, pack_version, pack_digest, query_id, kind, resolved_at
		FROM abom_suite_query
		ORDER BY suite, pack, query_id`)
	if err != nil {
		return nil, fmt.Errorf("reading suite contents: %w", err)
	}
	defer rows.Close()

	out := map[string][]SuiteQuery{}
	for rows.Next() {
		var suite string
		var q SuiteQuery
		if err := rows.Scan(
			&suite, &q.Pack, &q.PackVersion, &q.PackDigest, &q.QueryID, &q.Kind,
			&q.ResolvedAt); err != nil {
			return nil, fmt.Errorf("scanning a suite query: %w", err)
		}
		out[suite] = append(out[suite], q)
	}
	return out, rows.Err()
}
