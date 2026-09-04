package store

import (
	"fmt"
)

// Summary is the state of the whole pipeline, for an operator deciding what to run next.
type Summary struct {
	SeedRepos     int `json:"seedRepos"`
	UpstreamRepos int `json:"upstreamRepos"`
	Nodes         int `json:"nodes"`
	Edges         int `json:"edges"`
	Findings      int `json:"findings"`
	Rules         int `json:"rules"`
	// LinkedFindings are attached to a graph node; the rest are known only per repository.
	LinkedFindings int `json:"linkedFindings"`
	Advisories     int `json:"advisories"`
	// AffectedActions counts graph nodes an advisory covers.
	AffectedActions   int            `json:"affectedActions"`
	Exposures         int            `json:"exposures"`
	DirectExposures   int            `json:"directExposures"`
	Frontier          map[string]int `json:"frontier"`
	AnalyzedUpstreams int            `json:"analyzedUpstreams"`
}

// Summary counts what has been collected so far.
//
// Every stage is reported together because they are only meaningful against each other: edges
// without findings means the queries have not been run, and advisories without exposure means
// the reach has not been computed.
func (s *Store) Summary() (Summary, error) {
	out := Summary{Frontier: map[string]int{}}

	scalars := []struct {
		query string
		into  *int
		args  []any
	}{
		{`SELECT COUNT(*) FROM repository WHERE origin = ?`, &out.SeedRepos, []any{OriginSeed}},
		{`SELECT COUNT(*) FROM repository WHERE origin = ?`, &out.UpstreamRepos, []any{OriginUpstream}},
		{`SELECT COUNT(*) FROM abom_node`, &out.Nodes, nil},
		{`SELECT COUNT(*) FROM abom_edge`, &out.Edges, nil},
		{`SELECT COUNT(*) FROM abom_finding`, &out.Findings, nil},
		{`SELECT COUNT(DISTINCT rule_id) FROM abom_finding`, &out.Rules, nil},
		{`SELECT COUNT(DISTINCT finding_row_id) FROM abom_finding_node`, &out.LinkedFindings, nil},
		{`SELECT COUNT(*) FROM abom_advisory WHERE withdrawn_at IS NULL`, &out.Advisories, nil},
		{`SELECT COUNT(*) FROM abom_exposure`, &out.Exposures, nil},
		{`SELECT COUNT(*) FROM abom_exposure WHERE hops = 1`, &out.DirectExposures, nil},
		{`SELECT COUNT(*) FROM abom_frontier WHERE status = 'done'`, &out.AnalyzedUpstreams, nil},
		{`SELECT COUNT(DISTINCT n.row_id)
		  FROM abom_advisory a
		  JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		  JOIN abom_node n ON n.owner IS NOT NULL AND n.repo IS NOT NULL
		                  AND n.owner || '/' || n.repo = p.package_name
		  WHERE a.withdrawn_at IS NULL`, &out.AffectedActions, nil},
	}
	for _, sc := range scalars {
		if err := s.db.QueryRow(sc.query, sc.args...).Scan(sc.into); err != nil {
			return out, fmt.Errorf("summarizing: %w", err)
		}
	}

	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM abom_frontier GROUP BY status`)
	if err != nil {
		return out, fmt.Errorf("summarizing frontier: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return out, err
		}
		out.Frontier[status] = n
	}
	return out, rows.Err()
}
