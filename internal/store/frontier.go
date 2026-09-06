package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// FrontierItem is one dependency in the discovery queue, as the Discovery view shows it.
type FrontierItem struct {
	NodeKey string `json:"nodeKey"`
	// Ref is the reference this row analyzes. Empty means the default branch. A dependency used
	// at several refs has a row for each, because each is its own checkout and its own results.
	Ref    string `json:"ref"`
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Status string `json:"status"`
	// Depth is the distance from the seed set, which the Discovery view uses for placement.
	Depth int `json:"depth"`
	// Score is commonality per unit cost: what decides the order work is claimed in.
	Score          float64 `json:"score"`
	Attempts       int     `json:"attempts"`
	AnalysisCount  int     `json:"analysisCount"`
	ResolvedSHA    string  `json:"resolvedSha"`
	DiscoveredFrom string  `json:"discoveredFrom"`
	Reason         string  `json:"reason"`
	// AnalyzedAt dates the results on record. Empty means never analyzed, which is a different
	// statement from analyzed long ago and must stay tellable apart.
	AnalyzedAt string `json:"analyzedAt"`
	// Tier is what Depth means to a reader: whether this is theirs to fix, named directly by a
	// repository under review, or reached through an intermediary. It also decides which query
	// suites analyzed it. Derived from the same expression the tier counts use, so a row and the
	// header above it cannot disagree.
	Tier string `json:"tier"`
	// ClaimedAt dates the analysis in flight, so a worker stuck on one dependency shows up as
	// one that has been on it far too long.
	ClaimedAt string `json:"claimedAt"`
	// NextAttemptAt is when a failed dependency becomes claimable again.
	NextAttemptAt string `json:"nextAttemptAt"`
	// SupplyChain is "runtime" when something under review executes this, "build_time" when it
	// only runs in an upstream project's own CI.
	SupplyChain string `json:"supplyChain"`
	// Suites names what actually analyzed this row, as `pack@version`, from the runs recorded
	// against its commit. Never from the tier's current binding: that is what happens next, and
	// showing it would make a suite enabled this morning look retroactively applied.
	Suites []string `json:"suites"`
	// SuitesKnown separates "analyzed, and here is with what" from "analyzed before we recorded
	// what with". 1,555 of 1,707 runs on the real corpus predate pack identity, so the second is
	// the common case, and rendering it as no suites would assert that nothing looked.
	SuitesKnown bool `json:"suitesKnown"`
	// CLIVersion is the CodeQL that produced the row. Extraction and evaluation both change
	// between releases, so it is part of what a result means.
	CLIVersion string `json:"cliVersion"`
	// UsedBy names repositories running this exact version, bounded by usedBySample. The queue's
	// own discovered_from holds only the first repository to reach it, which is provenance for
	// the crawl rather than an answer to "who runs this".
	UsedBy []string `json:"usedBy"`
	// UsedByCount is how many there are in total, which is the number worth acting on:
	// actions/checkout@v4 is named by 220 repositories and the sample shows five.
	UsedByCount int `json:"usedByCount"`
	// UsedByOwned is how many of those the operator is verified against, and so can change
	// themselves. Reporting only the total reads as exposure they own, when on the real corpus
	// 220 repositories name actions/checkout@v4 and far fewer are theirs.
	UsedByOwned int `json:"usedByOwned"`
}

// usedBySample bounds the names carried inline. The count is exact; the list is a preview, so a
// page of 500 rows cannot turn into megabytes of repository names.
const usedBySample = 5

// selectUsedBy counts and samples the repositories naming a queue row's exact version.
//
// Matched on the edge's ref rather than the node, because one action at two references is two
// different things to everyone downstream and sharing users between them would undo that.
var selectUsedBy = `
	(SELECT COUNT(DISTINCT e.repository_row_id)
	   FROM abom_edge e
	   JOIN abom_node n ON n.row_id = e.dst_node_row_id
	  WHERE n.node_key = abom_frontier.node_key
	    AND COALESCE(e.ref_value, '') = abom_frontier.ref),
	(SELECT COUNT(DISTINCT e.repository_row_id)
	   FROM abom_edge e
	   JOIN abom_node n ON n.row_id = e.dst_node_row_id
	   JOIN repository r ON r.row_id = e.repository_row_id
	  WHERE n.node_key = abom_frontier.node_key
	    AND COALESCE(e.ref_value, '') = abom_frontier.ref
	    AND EXISTS (SELECT 1 FROM abom_account a
	                 WHERE LOWER(a.owner) = LOWER(SUBSTR(r.repository_full_name, 1,
	                       INSTR(r.repository_full_name, '/') - 1)))),
	(SELECT GROUP_CONCAT(name) FROM (
	   SELECT DISTINCT r.repository_full_name AS name
	     FROM abom_edge e
	     JOIN abom_node n ON n.row_id = e.dst_node_row_id
	     JOIN repository r ON r.row_id = e.repository_row_id
	    WHERE n.node_key = abom_frontier.node_key
	      AND COALESCE(e.ref_value, '') = abom_frontier.ref
	    ORDER BY name LIMIT ` + strconv.Itoa(usedBySample) + `))`

// runsForRow joins a queue row to the runs recorded against the very commit it analyzed.
//
// The empty-commit guards are load-bearing. Most rows have no `resolved_sha` and many runs have
// no `db_commit`, so without them the join degenerates into every commitless run matching every
// unanalyzed row, and a queued dependency would report suites it never saw.
const runsForRow = `
	FROM abom_query_run r
	WHERE r.repository = abom_frontier.owner || '/' || abom_frontier.repo
	  AND r.db_commit = abom_frontier.resolved_sha
	  AND COALESCE(abom_frontier.resolved_sha, '') <> ''
	  AND COALESCE(r.db_commit, '') <> ''`

// selectSuites renders the packs and the CodeQL version a row was analyzed with.
//
// GROUP_CONCAT skips nulls, so a run predating pack identity contributes nothing and the row
// comes back empty — which is the point: it is unrecorded, not suite-free.
const selectSuites = `
	(SELECT GROUP_CONCAT(DISTINCT r.pack || '@' || COALESCE(r.pack_version, '?'))` +
	runsForRow + ` AND COALESCE(r.pack, '') <> ''),
	(SELECT GROUP_CONCAT(DISTINCT r.cli_version)` + runsForRow + `)`

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

// RefCoverage counts the versions actually in use against the versions actually analyzed.
//
// This is the number the queue could not produce while it held one row per dependency: every
// reference was collapsed onto a single analysis of the default branch, so a repository pinning
// `v3` was reported from a reading of `main`. Observed counts distinct `name@ref` pairs the graph
// has seen; Analyzed counts those with results on record; Unqueued is the difference the crawl
// has not reached, which is a coverage gap rather than a clean result.
type RefCoverage struct {
	Observed int `json:"observed"`
	Analyzed int `json:"analyzed"`
	Queued   int `json:"queued"`
	Unqueued int `json:"unqueued"`
}

// observedRefs is every `name@ref` the graph has seen for something a crawl could fetch. Docker
// images are excluded for the same reason they are never queued: nothing can be cloned for one.
const observedRefs = `
	SELECT DISTINCT n.node_key AS node_key, COALESCE(e.ref_value, '') AS ref
	FROM abom_edge e
	JOIN abom_node n ON n.row_id = e.dst_node_row_id
	WHERE n.is_external = 1 AND n.node_kind IN ('action', 'reusable_workflow')`

// RefsCovered reports how much of what is in use has actually been analyzed.
func (s *Store) RefsCovered() (RefCoverage, error) {
	var out RefCoverage
	err := s.db.QueryRow(`
		WITH observed AS (`+observedRefs+`)
		SELECT COUNT(*),
		       COALESCE(SUM(f.status = 'done'), 0),
		       COALESCE(SUM(f.status IN ('pending', 'running')), 0),
		       COALESCE(SUM(f.node_key IS NULL), 0)
		FROM observed o
		LEFT JOIN abom_frontier f
		  ON f.node_key = o.node_key AND f.ref = o.ref`).
		Scan(&out.Observed, &out.Analyzed, &out.Queued, &out.Unqueued)
	if err != nil {
		return RefCoverage{}, fmt.Errorf("measuring reference coverage: %w", err)
	}
	return out, nil
}

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
		       COALESCE(claimed_at,''), COALESCE(next_attempt_at,''), supply_chain,
		       COALESCE(analyzed_at,''), ` + tierOfRow + `, ref,` + selectSuites + `,` +
		selectUsedBy + `
		FROM abom_frontier`
	where, args := frontierWhere(status)
	query += where
	// Same order Claim takes, so the queue reads as what happens next rather than as a ranking
	// the crawl is about to ignore.
	query += ` ORDER BY expedited_at IS NULL, score DESC, depth ASC, node_key`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	return queryAll(s, "reading frontier", func(r *sql.Rows) (FrontierItem, error) {
		var it FrontierItem
		var suites, cli sql.NullString
		var users sql.NullString
		if err := r.Scan(&it.NodeKey, &it.Owner, &it.Repo, &it.Status, &it.Depth,
			&it.Score, &it.Attempts, &it.AnalysisCount, &it.ResolvedSHA,
			&it.DiscoveredFrom, &it.Reason, &it.ClaimedAt, &it.NextAttemptAt, &it.SupplyChain,
			&it.AnalyzedAt, &it.Tier, &it.Ref, &suites, &cli,
			&it.UsedByCount, &it.UsedByOwned, &users); err != nil {
			return it, err
		}
		it.UsedBy = []string{}
		if users.Valid && users.String != "" {
			it.UsedBy = strings.Split(users.String, ",")
		}
		if suites.Valid && suites.String != "" {
			it.Suites = strings.Split(suites.String, ",")
			sort.Strings(it.Suites)
			it.SuitesKnown = true
		}
		it.CLIVersion = cli.String
		return it, nil
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
