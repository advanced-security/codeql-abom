package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// FindingsSuggestion is whether a run of some queries should store what it finds as findings, and
// why. One setting covers the whole run, so it is on only when every query earns it.
type FindingsSuggestion struct {
	Findings bool              `json:"findings"`
	Reason   string            `json:"reason"`
	Queries  []QuerySuggestion `json:"queries"`
}

// QuerySuggestion is what one query version contributes to a run's suggestion.
type QuerySuggestion struct {
	QueryID      string `json:"queryId"`
	QueryVersion string `json:"queryVersion"`
	// Promoted is a version unchanged from what a pack published or a suite promoted, whose results
	// that promotion already stands behind.
	Promoted bool   `json:"promoted"`
	Why      string `json:"why"`
}

// SuggestFindings says whether a run of these query versions should store findings. A version is
// promoted when an upstream pack published it, when somebody put that exact version at deploy, or
// when a suite a tier binds resolved to it, since the crawl already stores that one's results.
// Anything else is still being refined.
func (s *Store) SuggestFindings(queries []AnalysisQuery) (FindingsSuggestion, error) {
	if len(queries) == 0 {
		return FindingsSuggestion{}, errors.New("a run names at least one query, and none was named")
	}
	out := FindingsSuggestion{Findings: true, Queries: make([]QuerySuggestion, 0, len(queries))}
	var refining *QuerySuggestion
	for _, q := range queries {
		why, promoted, err := s.promotion(q)
		if err != nil {
			return out, err
		}
		out.Queries = append(out.Queries, QuerySuggestion{
			QueryID: q.QueryID, QueryVersion: q.QueryVersion, Promoted: promoted, Why: why,
		})
		if !promoted && refining == nil {
			refining = &out.Queries[len(out.Queries)-1]
		}
	}
	if refining != nil {
		out.Findings = false
		out.Reason = fmt.Sprintf("%s is still being refined (%s), so what this run finds is held "+
			"until somebody converts it", refining.QueryID, refining.Why)
		return out, nil
	}
	out.Reason = "Every query is a version a pack published or a suite promoted, so what this run " +
		"finds is stored as findings"
	return out, nil
}

// promotion says why a version is promoted, or why it is not.
func (s *Store) promotion(q AnalysisQuery) (string, bool, error) {
	if q.QueryVersion == "" {
		return "no version is recorded for it", false, nil
	}
	var pack string
	err := s.db.QueryRow(`
		SELECT pack FROM abom_suite_query
		WHERE query_id = ? AND query_version = ? AND pack LIKE 'codeql/%'
		ORDER BY pack LIMIT 1`, q.QueryID, q.QueryVersion).Scan(&pack)
	switch {
	case err == nil && sourceOfPack(pack) == QuerySourceUpstream:
		return "published in " + pack, true, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return "", false, fmt.Errorf("reading which pack published %s: %w", q.QueryID, err)
	}

	stages, err := s.stagesOf(q.QueryID)
	if err != nil {
		return "", false, err
	}
	if stages[q.QueryVersion] == StageDeploy {
		return "this version is at deploy", true, nil
	}

	var tiers []string
	rows, err := s.db.Query(`
		SELECT DISTINCT ts.tier FROM abom_suite_query sq
		JOIN abom_tier_suite ts ON ts.suite = sq.suite
		WHERE sq.query_id = ? AND sq.query_version = ?
		ORDER BY ts.tier`, q.QueryID, q.QueryVersion)
	if err != nil {
		return "", false, fmt.Errorf("reading which tiers run %s: %w", q.QueryID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var tier string
		if err := rows.Scan(&tier); err != nil {
			return "", false, fmt.Errorf("scanning a tier that runs %s: %w", q.QueryID, err)
		}
		tiers = append(tiers, tier)
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	if len(tiers) > 0 {
		return "the " + strings.Join(tiers, " and ") + " tier runs this version", true, nil
	}

	if stage := stages[q.QueryVersion]; stage != "" {
		return "this version is at " + stage, false, nil
	}
	for version, stage := range stages {
		if stage == StageDeploy && version != q.QueryVersion {
			return "changed since the version at deploy", false, nil
		}
	}
	return "nobody has put this version at any stage", false, nil
}

// stagesOf reads the stage of every version of one query somebody advanced.
func (s *Store) stagesOf(queryID string) (map[string]string, error) {
	out := map[string]string{}
	rows, err := s.db.Query(
		`SELECT query_version, stage FROM abom_query_stage WHERE query_id = ?`, queryID)
	if err != nil {
		return nil, fmt.Errorf("reading the stages of %s: %w", queryID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var version, stage string
		if err := rows.Scan(&version, &stage); err != nil {
			return nil, fmt.Errorf("scanning a stage of %s: %w", queryID, err)
		}
		out[version] = stage
	}
	return out, rows.Err()
}
