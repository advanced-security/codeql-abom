// Package frontier maintains the persisted queue of upstream dependencies awaiting analysis.
//
// Discovery is recursive: analyzing a dependency reveals further dependencies, so consumers of
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

// How a dependency comes to be part of the supply chain.
//
// Runtime means something under review executes it: a composite action's own steps run inside
// whoever calls the action, and a reusable workflow's steps run for whoever invokes it. Build
// means it only ever runs in an upstream project's own CI. Both are followed — compromising a
// release pipeline is how consumers receive a malicious version — but they are different claims,
// and reporting build-time work as though this organization ran it would overstate the exposure.
const (
	SupplyChainRuntime = "runtime"
	SupplyChainBuild   = "build_time"
)

// Item is one upstream dependency awaiting analysis.
type Item struct {
	NodeKey string
	// Ref is the reference to analyze. Empty means the default branch. A dependency used at
	// several references is several items: each is a different tree and its own results.
	Ref     string
	Owner   string
	Repo    string
	Subpath string
	// Depth is the shortest observed distance from the seed set, used only as a bound.
	Depth int
	// CostEstimate scales the score; zero means one unit.
	CostEstimate   float64
	DiscoveredFrom string
	// SupplyChain is empty for work seeded from a repository under review, which is runtime by
	// definition; Enqueue fills it in.
	SupplyChain string
}

// DefaultContainerWeight favors dependencies whose analysis can actually extend the graph.
//
// A reusable workflow or composite action always contains steps that its callers inherit, so
// analyzing one adds edges. Most plain actions turn out to be JavaScript, whose dependencies
// are npm packages an actions database cannot see, so analyzing them adds nothing. Of the first
// thirteen upstream repositories analyzed, only four contributed any composite steps.
//
// The weight multiplies commonality rather than replacing it, so what the organization most
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
			(node_key, ref, owner, repo, subpath, depth, status, cost_estimate,
			 discovered_from, enqueued_at, reason, completed_at, supply_chain)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_key, ref) DO UPDATE SET
			depth = MIN(abom_frontier.depth, excluded.depth),
			-- Provenance is only ever filled in, never replaced: the first repository to name a
			-- dependency is why it is here, and a row seeded before this was recorded would
			-- otherwise keep an empty answer forever.
			discovered_from = COALESCE(abom_frontier.discovered_from, excluded.discovered_from),
			-- Runtime is the stronger claim and wins, so the order repositories happen to be
			-- analyzed in cannot decide how a dependency is reported.
			supply_chain = CASE
				WHEN abom_frontier.supply_chain = ? OR excluded.supply_chain = ? THEN ?
				ELSE excluded.supply_chain
			END`)
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
		if err := tx.QueryRow(`SELECT 1 FROM abom_frontier WHERE node_key = ? AND ref = ?`,
			it.NodeKey, it.Ref).
			Scan(new(int)); err == nil {
			existed = true
		} else if err != sql.ErrNoRows {
			return added, fmt.Errorf("checking %q: %w", it.NodeKey, err)
		}

		status, reason := StatusPending, ""
		// Terminal on arrival, so it is dated on arrival: every other settled row carries the
		// time it stopped moving, and a view that shows one cannot leave these blank.
		completed := ""
		if !namesARepository(it.Owner, it.Repo) {
			status = StatusUnavailable
			reason = "the reference names no repository, so nothing can be fetched for it"
			completed = q.now().Format(time.RFC3339Nano)
		}

		chain := it.SupplyChain
		if chain == "" {
			chain = SupplyChainRuntime
		}

		if _, err := insert.Exec(it.NodeKey, it.Ref, nullable(it.Owner), nullable(it.Repo),
			nullable(it.Subpath), it.Depth, status, cost,
			nullable(it.DiscoveredFrom), q.now().Format(time.RFC3339Nano),
			nullable(reason), nullable(completed), chain,
			SupplyChainRuntime, SupplyChainRuntime, SupplyChainRuntime); err != nil {
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

// namesARepository reports whether owner/repo could address a real repository.
//
// A `uses:` assembled from an expression extracts with the expression where the account should
// be, giving keys like `$/bump-version`. Recording the reference verbatim is right — it is what
// the workflow says — but no such repository exists, so a checkout only rediscovers that.
func namesARepository(owner, repo string) bool {
	if owner == "" || repo == "" || len(owner) > 39 {
		return false
	}
	for _, r := range owner {
		if !isASCIILetterOrDigit(r) && r != '-' {
			return false
		}
	}
	for _, r := range repo {
		if !isASCIILetterOrDigit(r) && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func isASCIILetterOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
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
		SELECT node_key, ref, owner, repo, subpath, depth, cost_estimate, discovered_from
		FROM abom_frontier
		WHERE status = ?
		  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		ORDER BY expedited_at IS NULL, score DESC, depth ASC, enqueued_at ASC
		LIMIT ?`, StatusPending, now, limit)
	if err != nil {
		return nil, fmt.Errorf("claiming frontier items: %w", err)
	}

	var claimed []Item
	for rows.Next() {
		var it Item
		var owner, repo, subpath, from sql.NullString
		if err := rows.Scan(&it.NodeKey, &it.Ref, &owner, &repo, &subpath, &it.Depth,
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
			`UPDATE abom_frontier SET status = ?, claimed_at = ? WHERE node_key = ? AND ref = ?`,
			StatusRunning, now, it.NodeKey, it.Ref); err != nil {
			return nil, fmt.Errorf("marking %q running: %w", it.NodeKey, err)
		}
	}
	return claimed, nil
}

// ReclaimStale returns claims older than lease to the queue.
//
// Nothing else ever moves a running item: Claim only looks at pending, so an interrupted run
// strands its whole batch permanently. A claim with no recorded time predates the lease and is
// therefore from an earlier process by construction.
//
// No attempt is counted. An interrupted attempt taught us nothing about the dependency, and
// charging it would let a few restarts exhaust the retry budget of work never actually tried.
func (q *Queue) ReclaimStale(lease time.Duration) (int, error) {
	cutoff := q.now().Add(-lease).Format(time.RFC3339Nano)
	res, err := q.Store.DB().Exec(`
		UPDATE abom_frontier
		SET status = ?, claimed_at = NULL, next_attempt_at = NULL
		WHERE status = ? AND (claimed_at IS NULL OR claimed_at <= ?)`,
		StatusPending, StatusRunning, cutoff)
	if err != nil {
		return 0, fmt.Errorf("reclaiming stale claims: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// ReclaimOrphaned returns every claim to the queue, however recently it was made.
//
// Safe only at process start, and correct there: the store admits a single writer, so a claim
// that outlived the process holding it belongs to nobody. Honoring the lease instead leaves a
// stopped queue reporting dependencies as being analyzed for as long as an hour.
func (q *Queue) ReclaimOrphaned() (int, error) {
	return q.ReclaimStale(0)
}

// NextAttemptAt reports when the queue next holds claimable work, and false when nothing is
// pending at all. A time in the past means work is claimable now.
//
// Claim excludes items still in backoff, so a caller that treats an empty claim as an empty
// queue abandons that work while reporting the run finished.
func (q *Queue) NextAttemptAt() (time.Time, bool, error) {
	var at sql.NullString
	// MIN over an empty set still yields one row, holding NULL, so a drained queue is not
	// ErrNoRows and must be told apart by the null.
	if err := q.Store.DB().QueryRow(`
		SELECT MIN(COALESCE(next_attempt_at, enqueued_at))
		FROM abom_frontier WHERE status = ?`, StatusPending).Scan(&at); err != nil {
		return time.Time{}, false, fmt.Errorf("reading next attempt time: %w", err)
	}
	if !at.Valid {
		return time.Time{}, false, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, at.String)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parsing next attempt time %q: %w", at.String, err)
	}
	return parsed, true, nil
}

// HeadRef is the default thing to analyze: what the action does now.
const HeadRef = "HEAD"

// Resolution records which ref was analyzed and the commit it pointed at.
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
	// Requested is the reference the row is for, which is what addresses it. Ref and SHA below
	// describe the analysis currently in the graph, which is a different thing: a row requesting
	// `v4` records whatever commit `v4` pointed at when it was analyzed.
	Requested string
	Ref       string
	SHA       string
	Score     float64
}

// Key identifies one unit of work: a dependency at a reference.
//
// A type rather than two strings because every statement that addresses a row has to carry both.
// While the queue was keyed by name alone, one `WHERE node_key = ?` was correct; with several
// references per dependency the same statement silently updates all of them.
type Key struct {
	NodeKey string
	// Ref is empty for the default branch, which is what everything analyzed before references
	// were tracked was analyzed at.
	Ref string
}

// At names a dependency at a reference; Of names it at its default branch.
func At(nodeKey, ref string) Key { return Key{NodeKey: nodeKey, Ref: ref} }

// Of is the default branch, spelled out so a caller that means it says so.
func Of(nodeKey string) Key { return Key{NodeKey: nodeKey} }

// Key is how an item addresses its own row.
func (i Item) Key() Key { return Key{NodeKey: i.NodeKey, Ref: i.Ref} }

// Key is how a recheck addresses the row it came from.
func (r Recheck) Key() Key { return Key{NodeKey: r.NodeKey, Ref: r.Requested} }

// Complete marks an item analyzed and records what was analyzed.
func (q *Queue) Complete(k Key, res Resolution) error {
	if res.Ref == "" {
		res.Ref = HeadRef
	}
	now := q.now().Format(time.RFC3339Nano)
	_, err := q.Store.DB().Exec(`
		UPDATE abom_frontier
		SET status = ?, completed_at = ?, next_attempt_at = NULL, reason = NULL,
		    claimed_at = NULL, expedited_at = NULL,
		    analyzed_ref = ?, resolved_sha = ?, analyzed_at = ?, checked_at = ?,
		    analysis_count = analysis_count + 1
		WHERE node_key = ? AND ref = ?`,
		StatusDone, now, res.Ref, nullable(res.SHA), now, now, k.NodeKey, k.Ref)
	if err != nil {
		return fmt.Errorf("completing %q: %w", k.NodeKey, err)
	}
	return nil
}

// DueForRecheck returns analyzed dependencies whose ref may have moved since, most valuable
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
		       COALESCE(analyzed_ref, ''), COALESCE(resolved_sha, ''), score, ref
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
		if err := rows.Scan(&r.NodeKey, &owner, &repo, &subpath, &r.Ref, &r.SHA, &r.Score,
			&r.Requested); err != nil {
			return nil, fmt.Errorf("scanning recheck candidate: %w", err)
		}
		r.Owner, r.Repo, r.Subpath = owner.String, repo.String, subpath.String
		due = append(due, r)
	}
	return due, rows.Err()
}

// Touch records that a ref was probed and had not moved.
func (q *Queue) Touch(k Key) error {
	_, err := q.Store.DB().Exec(
		`UPDATE abom_frontier SET checked_at = ? WHERE node_key = ? AND ref = ?`,
		q.now().Format(time.RFC3339Nano), k.NodeKey, k.Ref)
	if err != nil {
		return fmt.Errorf("touching %q: %w", k.NodeKey, err)
	}
	return nil
}

// MarkStale requeues a dependency whose ref has moved.
//
// resolved_sha is deliberately left alone: it names the commit the stored graph came from, and
// stays accurate until re-analysis actually replaces that data.
func (q *Queue) MarkStale(k Key, newSHA string) error {
	_, err := q.Store.DB().Exec(`
		UPDATE abom_frontier
		SET status = ?, reason = ?, checked_at = ?,
		    completed_at = NULL, next_attempt_at = NULL, attempts = 0
		WHERE node_key = ? AND ref = ?`,
		StatusPending, fmt.Sprintf("ref moved to %s", newSHA),
		q.now().Format(time.RFC3339Nano), k.NodeKey, k.Ref)
	if err != nil {
		return fmt.Errorf("marking %q stale: %w", k.NodeKey, err)
	}
	return nil
}

// Release returns a claimed item to the queue without counting an attempt.
func (q *Queue) Release(k Key) error {
	return q.setStatus(k, StatusPending, "", "")
}

// Fail records a transient failure and schedules a retry.
func (q *Queue) Fail(k Key, reason string) error {
	var attempts int
	if err := q.Store.DB().QueryRow(
		`SELECT attempts FROM abom_frontier WHERE node_key = ? AND ref = ?`,
		k.NodeKey, k.Ref).Scan(&attempts); err != nil {
		return fmt.Errorf("reading attempts for %q: %w", k.NodeKey, err)
	}
	attempts++

	retryAt := q.now().Add(backoff(attempts)).Format(time.RFC3339Nano)
	_, err := q.Store.DB().Exec(`
		UPDATE abom_frontier
		SET status = ?, attempts = ?, reason = ?, next_attempt_at = ?, claimed_at = NULL
		WHERE node_key = ? AND ref = ?`,
		StatusPending, attempts, nullable(reason), retryAt, k.NodeKey, k.Ref)
	if err != nil {
		return fmt.Errorf("failing %q: %w", k.NodeKey, err)
	}
	return nil
}

// Unavailable records a permanent answer, such as a repository with no workflows and no action
// manifest. It is terminal so the crawler does not re-check it every run.
func (q *Queue) Unavailable(k Key, reason string) error {
	return q.setStatus(k, StatusUnavailable, reason, q.now().Format(time.RFC3339Nano))
}

func (q *Queue) setStatus(k Key, status, reason, completedAt string) error {
	var completed any
	if completedAt != "" {
		completed = completedAt
	}
	_, err := q.Store.DB().Exec(`
		UPDATE abom_frontier
		SET status = ?, reason = COALESCE(?, reason), completed_at = ?, next_attempt_at = NULL,
		    claimed_at = NULL
		WHERE node_key = ? AND ref = ?`,
		status, nullable(reason), completed, k.NodeKey, k.Ref)
	if err != nil {
		return fmt.Errorf("setting %q to %s: %w", k.NodeKey, status, err)
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
