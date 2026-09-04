// Package frontier maintains the persisted queue of upstream dependencies awaiting analysis.
//
// Discovery is recursive: analysing a dependency reveals further dependencies, so consumers of
// this queue are also its producers and expansion may span weeks across process restarts.
// That rules out a fixed batch and requires durable state.
//
// Ordering is greedy graph exploration under budget. Items are ranked by commonality per unit
// of cost, because the value of resolving a dependency comes from how many workflows and
// repositories depend on it. Depth deliberately does not influence priority: depth is
// path-dependent (the same action may be one hop away via one branch and four via another),
// whereas commonality is a property of the dependency itself. Depth only bounds exploration.
package frontier

import (
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Queue states.
const (
	StatusPending     = "pending"
	StatusRunning     = "running"
	StatusDone        = "done"
	StatusUnavailable = "unavailable"
)

// DefaultMaxDepth bounds how far from the seed repositories exploration may run.
const DefaultMaxDepth = 5

// Item is one upstream dependency awaiting analysis.
type Item struct {
	NodeKey string
	Owner   string
	Repo    string
	Subpath string
	// Depth is the shortest observed distance from the seed set, used only as a bound.
	Depth int
	// CostEstimate scales the score; zero means one unit.
	CostEstimate   float64
	DiscoveredFrom string
}

// DefaultContainerWeight favours dependencies whose analysis can actually extend the graph.
//
// A reusable workflow or composite action always contains steps that its callers inherit, so
// analysing one adds edges. Most plain actions turn out to be JavaScript, whose dependencies
// are npm packages an actions database cannot see, so analysing them adds nothing. Of the first
// thirteen upstream repositories analysed, only four contributed any composite steps.
//
// The weight multiplies commonality rather than replacing it, so what the organisation most
// depends on still comes first.
const DefaultContainerWeight = 3.0

// Queue is the durable frontier.
type Queue struct {
	Store    *store.Store
	MaxDepth int
	// ContainerWeight scales dependencies known to hold steps. One disables the preference.
	ContainerWeight float64
	// Now is overridable in tests.
	Now func() time.Time
}

func (q *Queue) containerWeight() float64 {
	if q.ContainerWeight <= 0 {
		return DefaultContainerWeight
	}
	return q.ContainerWeight
}

func (q *Queue) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now().UTC()
}

func (q *Queue) maxDepth() int {
	if q.MaxDepth <= 0 {
		return DefaultMaxDepth
	}
	return q.MaxDepth
}

// Enqueue adds newly discovered dependencies, returning how many were new.
//
// Re-discovery is the normal case in a recursive crawl, so an existing entry is never reset:
// only its depth is lowered if a shorter path has been found.
func (q *Queue) Enqueue(items []Item) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	tx, err := q.Store.DB().Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	insert, err := tx.Prepare(`
		INSERT INTO abom_frontier
			(node_key, owner, repo, subpath, depth, status, cost_estimate,
			 discovered_from, enqueued_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_key) DO UPDATE SET
			depth = MIN(abom_frontier.depth, excluded.depth)`)
	if err != nil {
		return 0, fmt.Errorf("preparing enqueue: %w", err)
	}
	defer insert.Close()

	added := 0
	for _, it := range items {
		if it.NodeKey == "" || it.Depth > q.maxDepth() {
			continue
		}
		cost := it.CostEstimate
		if cost <= 0 {
			cost = 1
		}

		var existed bool
		if err := tx.QueryRow(`SELECT 1 FROM abom_frontier WHERE node_key = ?`, it.NodeKey).
			Scan(new(int)); err == nil {
			existed = true
		} else if err != sql.ErrNoRows {
			return added, fmt.Errorf("checking %q: %w", it.NodeKey, err)
		}

		if _, err := insert.Exec(it.NodeKey, nullable(it.Owner), nullable(it.Repo),
			nullable(it.Subpath), it.Depth, StatusPending, cost,
			nullable(it.DiscoveredFrom), q.now().Format(time.RFC3339Nano)); err != nil {
			return added, fmt.Errorf("enqueueing %q: %w", it.NodeKey, err)
		}
		if !existed {
			added++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return added, nil
}

// Rescore recomputes priorities from current commonality.
//
// A dependency's commonality is only partly known when first discovered, so scores must be
// refreshed as the graph fills in; that self-correction is what makes greedy exploration
// adaptive rather than committed to its first guess.
func (q *Queue) Rescore() error {
	_, err := q.Store.DB().Exec(`
		UPDATE abom_frontier
		SET score = COALESCE((
			SELECT (b.dependent_workflows + b.dependent_repos)
			       * CASE n.node_kind
			             WHEN 'reusable_workflow' THEN ?
			             WHEN 'composite_action'  THEN ?
			             ELSE 1.0
			         END
			       / abom_frontier.cost_estimate
			FROM abom_node n
			JOIN abom_blast_radius b ON b.node_row_id = n.row_id
			WHERE n.node_key = abom_frontier.node_key
		), 0)`, q.containerWeight(), q.containerWeight())
	if err != nil {
		return fmt.Errorf("rescoring frontier: %w", err)
	}
	return nil
}

// Claim takes up to limit of the highest-value pending items and marks them running.
func (q *Queue) Claim(limit int) ([]Item, error) {
	if limit <= 0 {
		return nil, nil
	}
	now := q.now().Format(time.RFC3339Nano)

	rows, err := q.Store.DB().Query(`
		SELECT node_key, owner, repo, subpath, depth, cost_estimate, discovered_from
		FROM abom_frontier
		WHERE status = ?
		  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		ORDER BY score DESC, depth ASC, enqueued_at ASC
		LIMIT ?`, StatusPending, now, limit)
	if err != nil {
		return nil, fmt.Errorf("claiming frontier items: %w", err)
	}

	var claimed []Item
	for rows.Next() {
		var it Item
		var owner, repo, subpath, from sql.NullString
		if err := rows.Scan(&it.NodeKey, &owner, &repo, &subpath, &it.Depth,
			&it.CostEstimate, &from); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning frontier item: %w", err)
		}
		it.Owner, it.Repo, it.Subpath, it.DiscoveredFrom =
			owner.String, repo.String, subpath.String, from.String
		claimed = append(claimed, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, it := range claimed {
		if _, err := q.Store.DB().Exec(
			`UPDATE abom_frontier SET status = ? WHERE node_key = ?`,
			StatusRunning, it.NodeKey); err != nil {
			return nil, fmt.Errorf("marking %q running: %w", it.NodeKey, err)
		}
	}
	return claimed, nil
}

// HeadRef is the default thing to analyse: what the action does now.
const HeadRef = "HEAD"

// Resolution records which ref was analysed and the commit it pointed at.
type Resolution struct {
	// Ref is what was requested, usually HeadRef.
	Ref string
	// SHA is what Ref resolved to at analysis time.
	SHA string
}

// Mutable reports whether the ref can move under us. A ref that is identical to the commit it
// resolved to is immutable by construction, which avoids having to classify ref syntax.
func (r Resolution) Mutable() bool {
	return r.SHA != "" && r.Ref != r.SHA
}

// Recheck is a completed item whose ref should be probed for drift.
type Recheck struct {
	NodeKey string
	Owner   string
	Repo    string
	Subpath string
	// Ref and SHA describe the analysis currently in the graph.
	Ref   string
	SHA   string
	Score float64
}

// Complete marks an item analysed and records what was analysed.
func (q *Queue) Complete(nodeKey string, res Resolution) error {
	if res.Ref == "" {
		res.Ref = HeadRef
	}
	now := q.now().Format(time.RFC3339Nano)
	_, err := q.Store.DB().Exec(`
		UPDATE abom_frontier
		SET status = ?, completed_at = ?, next_attempt_at = NULL, reason = NULL,
		    analyzed_ref = ?, resolved_sha = ?, analyzed_at = ?, checked_at = ?,
		    analysis_count = analysis_count + 1
		WHERE node_key = ?`,
		StatusDone, now, res.Ref, nullable(res.SHA), now, now, nodeKey)
	if err != nil {
		return fmt.Errorf("completing %q: %w", nodeKey, err)
	}
	return nil
}

// DueForRecheck returns analysed dependencies whose ref may have moved since, most valuable
// first, so a cheap ref probe is spent on what most of the corpus depends on.
//
// Immutable refs are excluded: they cannot drift, so re-probing them is pure waste.
func (q *Queue) DueForRecheck(ttl time.Duration, limit int) ([]Recheck, error) {
	if limit <= 0 {
		return nil, nil
	}
	cutoff := q.now().Add(-ttl).Format(time.RFC3339Nano)

	rows, err := q.Store.DB().Query(`
		SELECT node_key, owner, repo, subpath,
		       COALESCE(analyzed_ref, ''), COALESCE(resolved_sha, ''), score
		FROM abom_frontier
		WHERE status = ?
		  AND resolved_sha IS NOT NULL AND resolved_sha <> ''
		  AND (analyzed_ref IS NULL OR analyzed_ref <> resolved_sha)
		  AND (checked_at IS NULL OR checked_at <= ?)
		ORDER BY score DESC, checked_at ASC
		LIMIT ?`, StatusDone, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("finding recheck candidates: %w", err)
	}
	defer rows.Close()

	var due []Recheck
	for rows.Next() {
		var r Recheck
		var owner, repo, subpath sql.NullString
		if err := rows.Scan(&r.NodeKey, &owner, &repo, &subpath, &r.Ref, &r.SHA, &r.Score); err != nil {
			return nil, fmt.Errorf("scanning recheck candidate: %w", err)
		}
		r.Owner, r.Repo, r.Subpath = owner.String, repo.String, subpath.String
		due = append(due, r)
	}
	return due, rows.Err()
}

// Touch records that a ref was probed and had not moved.
func (q *Queue) Touch(nodeKey string) error {
	_, err := q.Store.DB().Exec(`UPDATE abom_frontier SET checked_at = ? WHERE node_key = ?`,
		q.now().Format(time.RFC3339Nano), nodeKey)
	if err != nil {
		return fmt.Errorf("touching %q: %w", nodeKey, err)
	}
	return nil
}

// MarkStale requeues a dependency whose ref has moved.
//
// resolved_sha is deliberately left alone: it names the commit the stored graph came from, and
// stays accurate until re-analysis actually replaces that data.
func (q *Queue) MarkStale(nodeKey, newSHA string) error {
	_, err := q.Store.DB().Exec(`
		UPDATE abom_frontier
		SET status = ?, reason = ?, checked_at = ?,
		    completed_at = NULL, next_attempt_at = NULL, attempts = 0
		WHERE node_key = ?`,
		StatusPending, fmt.Sprintf("ref moved to %s", newSHA),
		q.now().Format(time.RFC3339Nano), nodeKey)
	if err != nil {
		return fmt.Errorf("marking %q stale: %w", nodeKey, err)
	}
	return nil
}

// Release returns a claimed item to the queue without counting an attempt.
func (q *Queue) Release(nodeKey string) error {
	return q.setStatus(nodeKey, StatusPending, "", "", nil)
}

// Fail records a transient failure and schedules a retry.
func (q *Queue) Fail(nodeKey, reason string) error {
	var attempts int
	if err := q.Store.DB().QueryRow(
		`SELECT attempts FROM abom_frontier WHERE node_key = ?`, nodeKey).Scan(&attempts); err != nil {
		return fmt.Errorf("reading attempts for %q: %w", nodeKey, err)
	}
	attempts++

	retryAt := q.now().Add(backoff(attempts)).Format(time.RFC3339Nano)
	_, err := q.Store.DB().Exec(`
		UPDATE abom_frontier
		SET status = ?, attempts = ?, reason = ?, next_attempt_at = ?
		WHERE node_key = ?`, StatusPending, attempts, nullable(reason), retryAt, nodeKey)
	if err != nil {
		return fmt.Errorf("failing %q: %w", nodeKey, err)
	}
	return nil
}

// Unavailable records a permanent answer, such as a repository with no workflows and no action
// manifest. It is terminal so the crawler does not re-check it every run.
func (q *Queue) Unavailable(nodeKey, reason string) error {
	return q.setStatus(nodeKey, StatusUnavailable, reason,
		q.now().Format(time.RFC3339Nano), nil)
}

func (q *Queue) setStatus(nodeKey, status, reason, completedAt string, _ error) error {
	var completed any
	if completedAt != "" {
		completed = completedAt
	}
	_, err := q.Store.DB().Exec(`
		UPDATE abom_frontier
		SET status = ?, reason = COALESCE(?, reason), completed_at = ?, next_attempt_at = NULL
		WHERE node_key = ?`, status, nullable(reason), completed, nodeKey)
	if err != nil {
		return fmt.Errorf("setting %q to %s: %w", nodeKey, status, err)
	}
	return nil
}

// Stats counts items by status.
func (q *Queue) Stats() (map[string]int, error) {
	rows, err := q.Store.DB().Query(
		`SELECT status, COUNT(*) FROM abom_frontier GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("counting frontier: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

// backoff grows exponentially and is capped so a long run never parks work indefinitely.
func backoff(attempts int) time.Duration {
	const cap = 24 * time.Hour
	d := time.Duration(math.Pow(2, float64(attempts))) * time.Minute
	if d > cap || d <= 0 {
		return cap
	}
	return d
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
