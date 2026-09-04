package store

import (
	"database/sql"
	"fmt"
)

// FrontierItem is one dependency in the discovery queue, as the Discovery view shows it.
type FrontierItem struct {
	NodeKey string `json:"nodeKey"`
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	Status  string `json:"status"`
	// Depth is the distance from the seed set, which the Discovery view uses for placement.
	Depth int `json:"depth"`
	// Score is commonality per unit cost: what decides the order work is claimed in.
	Score          float64 `json:"score"`
	Attempts       int     `json:"attempts"`
	AnalysisCount  int     `json:"analysisCount"`
	ResolvedSHA    string  `json:"resolvedSha"`
	DiscoveredFrom string  `json:"discoveredFrom"`
	Reason         string  `json:"reason"`
}

// FrontierSnapshot returns the discovery queue, highest value first.
//
// Passing an empty status returns every state, because the Discovery view is about the shape of
// what is known against what is not: showing only pending work would hide the progress.
func (s *Store) FrontierSnapshot(status string, limit int) ([]FrontierItem, error) {
	query := `
		SELECT node_key, COALESCE(owner,''), COALESCE(repo,''), status, depth, score,
		       attempts, analysis_count, COALESCE(resolved_sha,''),
		       COALESCE(discovered_from,''), COALESCE(reason,'')
		FROM abom_frontier`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY score DESC, depth ASC, node_key`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("reading frontier: %w", err)
	}
	defer rows.Close()

	var out []FrontierItem
	for rows.Next() {
		var it FrontierItem
		if err := rows.Scan(&it.NodeKey, &it.Owner, &it.Repo, &it.Status, &it.Depth,
			&it.Score, &it.Attempts, &it.AnalysisCount, &it.ResolvedSHA,
			&it.DiscoveredFrom, &it.Reason); err != nil {
			return nil, fmt.Errorf("scanning frontier item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

var _ = sql.ErrNoRows
