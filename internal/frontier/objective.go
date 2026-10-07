package frontier

import (
	"fmt"
	"sort"
)

// DepthProgress is how much of one depth has been settled.
//
// The unit of the objective: "complete at depth 1" is a claim the queue can prove, where "worked
// on the cheapest thing available" never was.
type DepthProgress struct {
	Depth int `json:"depth"`
	Total int `json:"total"`
	// Done counts rows analyzed. Settled counts those plus the ones nothing can act on, which is
	// what completeness is measured against.
	Done    int `json:"done"`
	Settled int `json:"settled"`
	// Nothing counts starting points read in full with nothing to analyze, settled and never
	// analyzed: they are part of Settled and not of Done.
	Nothing int `json:"nothing"`
	// A failed attempt returns to pending with a retry time, so there is no failed state to count.
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Available int `json:"available"`
	// Stuck counts pending rows past the attempt ceiling: never analyzed, never unavailable.
	Stuck int `json:"stuck"`
	// Refresh counts rows analyzed before whose reference has since moved. They are the third
	// stage's work and do not hold their depth open.
	Refresh int `json:"refresh"`
}

// Complete reports whether nothing at this depth is still to be analyzed for the first time, or
// stuck for now.
//
// A dependency GitHub has no database for stays `unavailable` forever; counting it as outstanding
// would make its depth permanently incomplete and the objective permanently unsatisfiable.
// A row stuck past the attempt ceiling would do the same, so it waits on its own retries instead.
// A reference that moved was analyzed once, and the stage that re-analyzes it comes after this.
func (d DepthProgress) Complete() bool { return d.Settled+d.Stuck+d.Refresh >= d.Total }

// Completeness reports progress at every depth inside the closure, shallowest first.
func (q *Queue) Completeness() ([]DepthProgress, error) {
	inside, bound, err := q.bound()
	if err != nil {
		return nil, err
	}
	// A refresh past the attempt ceiling is counted as stuck, and only there.
	rows, err := q.Store.DB().Query(`
		SELECT depth,
		       COUNT(*),
		       SUM(status = ?),
		       SUM(status IN (?, ?, ?)),
		       SUM(status = ?),
		       SUM(status = ?),
		       SUM(status = ?),
		       SUM(status = ? AND attempts >= ?),
		       SUM(requeued_for IS 'moved' AND NOT (status = ? AND attempts >= ?))
		FROM abom_frontier
		WHERE `+inside+`
		GROUP BY depth
		ORDER BY depth`,
		append([]any{StatusDone, StatusDone, StatusUnavailable, StatusNothing, StatusNothing,
			StatusPending, StatusRunning, StatusPending, q.stuckAfter(), StatusPending,
			q.stuckAfter()}, bound...)...)
	if err != nil {
		return nil, fmt.Errorf("reading completeness by depth: %w", err)
	}
	defer rows.Close()

	out := []DepthProgress{}
	for rows.Next() {
		var d DepthProgress
		if err := rows.Scan(&d.Depth, &d.Total, &d.Done, &d.Settled, &d.Nothing, &d.Pending,
			&d.Running, &d.Stuck, &d.Refresh); err != nil {
			return nil, fmt.Errorf("scanning depth progress: %w", err)
		}
		d.Available = d.Total - d.Settled
		out = append(out, d)
	}
	return out, rows.Err()
}

// ShallowestIncomplete names the depth the crawl is working on.
//
// The boolean distinguishes "everything is complete" from depth 0, which is a different and wrong
// statement — the same absent-versus-zero rule the rest of the interface is held to.
func ShallowestIncomplete(progress []DepthProgress) (int, bool) {
	sorted := append([]DepthProgress{}, progress...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Depth < sorted[j].Depth })
	for _, d := range sorted {
		if !d.Complete() {
			return d.Depth, true
		}
	}
	return 0, false
}

// DefaultStuckAfter is when a failing row stops holding its depth open: the eighth doubling backoff
// lands 254 minutes in, past four rate-limit hours.
const DefaultStuckAfter = 8

func (q *Queue) stuckAfter() int {
	if q.StuckAfter <= 0 {
		return DefaultStuckAfter
	}
	return q.StuckAfter
}

// StuckAfterAttempts is the ceiling this queue judges completeness by.
func (q *Queue) StuckAfterAttempts() int { return q.stuckAfter() }

// Drained reports whether nothing inside the closure is pending but rows stuck past the attempt
// ceiling.
func (q *Queue) Drained() (bool, error) {
	inside, bound, err := q.bound()
	if err != nil {
		return false, err
	}
	var waiting bool
	if err := q.Store.DB().QueryRow(`
		SELECT EXISTS (SELECT 1 FROM abom_frontier WHERE status = ? AND attempts < ? AND `+
		inside+`)`, append([]any{StatusPending, q.stuckAfter()}, bound...)...).
		Scan(&waiting); err != nil {
		return false, fmt.Errorf("reading whether the queue is drained: %w", err)
	}
	return !waiting, nil
}
