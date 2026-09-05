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
	// ClaimedAt dates the analysis in flight, so a worker stuck on one dependency shows up as
	// one that has been on it far too long.
	ClaimedAt string `json:"claimedAt"`
	// NextAttemptAt is when a failed dependency becomes claimable again.
	NextAttemptAt string `json:"nextAttemptAt"`
	// SupplyChain is "runtime" when something under review executes this, "build_time" when it
	// only runs in an upstream project's own CI.
	SupplyChain string `json:"supplyChain"`
}

// Pseudo-statuses splitting pending by whether anything has been tried.
//
// Both are pending to the queue, which is right: they are claimed the same way. They are not the
// same thing to a reader, because one has cost nothing yet and the other has already failed.
const (
	FrontierQueued   = "queued"
	FrontierRetrying = "retrying"
)

// FrontierAdvisories asks a different question from the rest: not where a dependency sits in the
// queue, but whether anything published names it. A dependency can be analyzed and advisory-named
// at once, so this cuts across the other filters rather than joining them.
const FrontierAdvisories = "advisories"

// namedByAdvisory matches a queue entry whose repository a live advisory names.
//
// Naming is all this asserts. Whether the reference actually in use falls inside the vulnerable
// range is not decided here, and must not be: the advisory is a statement, not a verdict.
const namedByAdvisory = `EXISTS (
	SELECT 1 FROM abom_advisory a
	JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
	WHERE a.withdrawn_at IS NULL
	  AND p.package_name = abom_frontier.owner || '/' || abom_frontier.repo)`

// FrontierUpstreamOnly counts dependencies the crawl will never take on: those an upstream
// project's own CI names and no repository under review does. Expansion skips them deliberately,
// because an upstream repository's release pipeline is not something this organization runs.
//
// It is counted anyway. They are drawn in the Explorer like anything else, so a drained queue
// that mentions only what it finished reads as a complete picture of a graph that still holds
// hundreds of dependencies nobody has looked inside.
const FrontierUpstreamOnly = "upstreamOnly"

// FrontierBuildTime counts queue entries that run only in an upstream project's own CI. They are
// analyzed like anything else — a compromised release pipeline is how a consumer receives a
// malicious version — but nothing under review executes them, and a count that folded them in
// with runtime dependencies would overstate what this organization actually runs.
const FrontierBuildTime = "buildTime"

// countUpstreamOnly is expressed against the graph rather than the queue, because the whole point
// is the dependencies the queue never received a row for. Docker images are excluded: nothing can
// be cloned for one, so it is out of scope for an unrelated reason.
const countUpstreamOnly = `
	SELECT COUNT(*) FROM abom_node n
	LEFT JOIN abom_frontier f ON f.node_key = n.node_key
	WHERE n.is_external = 1
	  AND n.node_kind IN ('action', 'reusable_workflow')
	  AND f.node_key IS NULL`

// frontierWhere renders a status filter, including the ones that are not stored statuses.
func frontierWhere(status string) (string, []any) {
	switch status {
	case "":
		return "", nil
	case FrontierQueued:
		return ` WHERE status = 'pending' AND attempts = 0`, nil
	case FrontierRetrying:
		return ` WHERE status = 'pending' AND attempts > 0`, nil
	case FrontierAdvisories:
		return ` WHERE ` + namedByAdvisory, nil
	case FrontierBuildTime:
		return ` WHERE supply_chain = 'build_time'`, nil
	default:
		return ` WHERE status = ?`, []any{status}
	}
}

// FrontierSnapshot returns the discovery queue, highest value first.
//
// Passing an empty status returns every state, because the Discovery view is about the shape of
// what is known against what is not: showing only pending work would hide the progress.
func (s *Store) FrontierSnapshot(status string, limit int) ([]FrontierItem, error) {
	query := `
		SELECT node_key, COALESCE(owner,''), COALESCE(repo,''), status, depth, score,
		       attempts, analysis_count, COALESCE(resolved_sha,''),
		       COALESCE(discovered_from,''), COALESCE(reason,''),
		       COALESCE(claimed_at,''), COALESCE(next_attempt_at,''), supply_chain
		FROM abom_frontier`
	where, args := frontierWhere(status)
	query += where
	query += ` ORDER BY score DESC, depth ASC, node_key`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	return queryAll(s, "reading frontier", func(r *sql.Rows) (FrontierItem, error) {
		var it FrontierItem
		return it, r.Scan(&it.NodeKey, &it.Owner, &it.Repo, &it.Status, &it.Depth,
			&it.Score, &it.Attempts, &it.AnalysisCount, &it.ResolvedSHA,
			&it.DiscoveredFrom, &it.Reason, &it.ClaimedAt, &it.NextAttemptAt, &it.SupplyChain)
	}, query, args...)
}

// FrontierCounts counts the queue by the states a reader distinguishes, which is one more than
// the queue has: a dependency waiting on a retry has already cost something.
func (s *Store) FrontierCounts() (map[string]int, error) {
	rows, err := s.db.Query(`
		SELECT CASE
		         WHEN status = 'pending' AND attempts > 0 THEN 'retrying'
		         WHEN status = 'pending' THEN 'queued'
		         ELSE status
		       END,
		       COUNT(*)
		FROM abom_frontier GROUP BY 1`)
	if err != nil {
		return nil, fmt.Errorf("counting the frontier: %w", err)
	}
	defer rows.Close()

	// Every state is present, so a caller can say "none" rather than leaving a gap that reads
	// as an unanswered question.
	out := map[string]int{
		FrontierQueued: 0, FrontierRetrying: 0, FrontierAdvisories: 0, FrontierUpstreamOnly: 0,
		FrontierBuildTime: 0,
		"running":         0, "done": 0, "unavailable": 0,
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Counted separately because it cuts across the others: these entries are already inside
	// one of the status counts above.
	var named int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM abom_frontier WHERE ` + namedByAdvisory).Scan(&named); err != nil {
		return nil, fmt.Errorf("counting advisory-named dependencies: %w", err)
	}
	out[FrontierAdvisories] = named

	var upstreamOnly int
	if err := s.db.QueryRow(countUpstreamOnly).Scan(&upstreamOnly); err != nil {
		return nil, fmt.Errorf("counting dependencies named only upstream: %w", err)
	}
	out[FrontierUpstreamOnly] = upstreamOnly

	var buildTime int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM abom_frontier WHERE supply_chain = 'build_time'`).
		Scan(&buildTime); err != nil {
		return nil, fmt.Errorf("counting build-time dependencies: %w", err)
	}
	out[FrontierBuildTime] = buildTime
	return out, nil
}
