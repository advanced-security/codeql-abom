package store

import (
	"fmt"
	"strconv"
	"time"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// QueryMetadata is what one version of a query declares about itself, every pair as
// `codeql resolve metadata` read it.
type QueryMetadata struct {
	QueryID      string
	QueryVersion string
	Pairs        map[string]string
}

// RecordQueryMetadata replaces what each version is known to declare.
//
// Keyed by the version, not the id: a new version may declare a different `@security-severity`,
// and the version a finding's evaluation ran is the one whose declaration it was reported under.
func (s *Store) RecordQueryMetadata(mds []QueryMetadata) error {
	if len(mds) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	for _, md := range mds {
		if md.QueryID == "" {
			return fmt.Errorf("store: query metadata needs the query id it describes")
		}
		if _, err := tx.Exec(`DELETE FROM abom_query_metadata WHERE query_id = ? AND query_version = ?`,
			md.QueryID, md.QueryVersion); err != nil {
			return fmt.Errorf("clearing what %s declared: %w", md.QueryID, err)
		}
		for name, value := range md.Pairs {
			if _, err := tx.Exec(`
				INSERT INTO abom_query_metadata (query_id, query_version, name, value, resolved_at)
				VALUES (?, ?, ?, ?, ?)`, md.QueryID, md.QueryVersion, name, value, now); err != nil {
				return fmt.Errorf("recording %s of %s: %w", name, md.QueryID, err)
			}
		}
	}
	return tx.Commit()
}

// queryMetadata reads every version's declaration, keyed by id and version.
func (s *Store) queryMetadata() (map[string]map[string]string, error) {
	rows, err := s.db.Query(`SELECT query_id, query_version, name, value FROM abom_query_metadata`)
	if err != nil {
		return nil, fmt.Errorf("reading query metadata: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var id, version, name, value string
		if err := rows.Scan(&id, &version, &name, &value); err != nil {
			return nil, fmt.Errorf("scanning query metadata: %w", err)
		}
		key := id + "\x00" + version
		if out[key] == nil {
			out[key] = map[string]string{}
		}
		out[key][name] = value
	}
	return out, rows.Err()
}

// declaredBand is the band a query's own `@security-severity` falls in, for a query that reports
// findings. A table or graph query reports none, so it has no band rather than an unknown one.
func declaredBand(pairs map[string]string) string {
	if kind := pairs["kind"]; kind != "problem" && kind != "path-problem" {
		return ""
	}
	var score *float64
	if v, err := strconv.ParseFloat(pairs["security-severity"], 64); err == nil {
		score = &v
	}
	return viewspec.BandOf(score)
}
