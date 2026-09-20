package store

import (
	"database/sql"
	"fmt"
)

// Summary is the state of the whole pipeline, for an operator deciding what to run next.
type Summary struct {
	SeedRepos int `json:"seedRepos"`
	// OwnedRepos are the seeded repositories the declaration claims. Reported separately because
	// the two are independent: the reference corpus holds 204 seeded and owns none of them, and
	// printing the first as the operator's is a claim of responsibility nobody made.
	OwnedRepos    int `json:"ownedRepos"`
	UpstreamRepos int `json:"upstreamRepos"`
	Nodes         int `json:"nodes"`
	// Dependencies are the nodes that are something depended on rather than part of the thing
	// doing the depending. Nodes counts steps, jobs and workflows too, and they outnumber the
	// dependencies fifty to one, so the two must never be printed under the same word.
	Dependencies int `json:"dependencies"`
	Edges        int `json:"edges"`
	Findings     int `json:"findings"`
	// OwnedFindings are the findings in repositories the declaration claims. The Dashboard prints
	// this one as findings "in what you run", and a finding in a repository nobody declared is not
	// in what you run.
	OwnedFindings int `json:"ownedFindings"`
	Rules         int `json:"rules"`
	// LinkedFindings are attached to a graph node; the rest are known only per repository.
	LinkedFindings int `json:"linkedFindings"`
	Advisories     int `json:"advisories"`
	// AdvisoriesFetchedAt is when advisory statements were last collected, empty if never.
	// Nothing in this tool fetches them, so their age is the difference between an action no
	// advisory names and one nobody has checked.
	AdvisoriesFetchedAt string `json:"advisoriesFetchedAt"`
	// AffectedActions counts graph nodes an advisory covers.
	AffectedActions int `json:"affectedActions"`
	Exposures       int `json:"exposures"`
	DirectExposures int `json:"directExposures"`
	// OwnedExposures are the routes that start in a repository the declaration claims, kept apart
	// from Exposures for the reason OwnedRepos is kept apart from SeedRepos. The reference corpus
	// holds 253 routes across 115 repositories and owns none of them, so presenting the corpus
	// figure as the operator's offers them work to do from repositories they cannot change.
	OwnedExposures       int            `json:"ownedExposures"`
	OwnedDirectExposures int            `json:"ownedDirectExposures"`
	Frontier             map[string]int `json:"frontier"`
	AnalyzedUpstreams    int            `json:"analyzedUpstreams"`
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
		{`SELECT COUNT(*) FROM repository WHERE ` + ownedFullName("repository_full_name"),
			&out.OwnedRepos, nil},
		{`SELECT COUNT(*) FROM repository WHERE origin = ?`, &out.UpstreamRepos, []any{OriginUpstream}},
		{`SELECT COUNT(*) FROM abom_node_live`, &out.Nodes, nil},
		// The Inventory's kinds, spelled the same way, because the Dashboard card counting this
		// links straight to that unit and two definitions would disagree on arrival.
		{`SELECT COUNT(*) FROM abom_node_live
		   WHERE node_kind IN ('action', 'composite_action', 'docker_image')`,
			&out.Dependencies, nil},
		{`SELECT COUNT(*) FROM abom_edge_regular`, &out.Edges, nil},
		{`SELECT COUNT(*) FROM abom_finding_regular`, &out.Findings, nil},
		{`SELECT COUNT(*) FROM abom_finding_regular f
		  JOIN repository r ON r.row_id = f.repository_row_id
		  WHERE ` + OwnedRepositoryFilter, &out.OwnedFindings, nil},
		{`SELECT COUNT(DISTINCT rule_id) FROM abom_finding_regular`, &out.Rules, nil},
		{`SELECT COUNT(DISTINCT finding_row_id) FROM abom_finding_node`, &out.LinkedFindings, nil},
		{`SELECT COUNT(*) FROM abom_advisory WHERE withdrawn_at IS NULL`, &out.Advisories, nil},
		{`SELECT COUNT(*) FROM abom_exposure`, &out.Exposures, nil},
		{`SELECT COUNT(*) FROM abom_exposure WHERE hops = 1`, &out.DirectExposures, nil},
		{`SELECT COUNT(*) FROM abom_exposure x
		  JOIN repository r ON r.row_id = x.repository_row_id
		  WHERE ` + OwnedRepositoryFilter, &out.OwnedExposures, nil},
		{`SELECT COUNT(*) FROM abom_exposure x
		  JOIN repository r ON r.row_id = x.repository_row_id
		  WHERE x.hops = 1 AND ` + OwnedRepositoryFilter, &out.OwnedDirectExposures, nil},
		{`SELECT COUNT(*) FROM abom_frontier WHERE status = 'done'`, &out.AnalyzedUpstreams, nil},
		{`SELECT COUNT(DISTINCT n.row_id)
		  FROM abom_advisory a
		  JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		  JOIN abom_node_live n ON n.owner IS NOT NULL AND n.repo IS NOT NULL
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
	if err := rows.Err(); err != nil {
		return out, err
	}

	var fetched sql.NullString
	if err := s.db.QueryRow(`SELECT MAX(fetched_at) FROM abom_advisory`).Scan(&fetched); err != nil {
		return out, fmt.Errorf("summarizing advisory age: %w", err)
	}
	out.AdvisoriesFetchedAt = fetched.String
	return out, nil
}
