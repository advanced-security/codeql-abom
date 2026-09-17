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
	// A failed attempt returns to pending with a retry time, so there is no failed state to count.
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Available int `json:"available"`
}

// Complete reports whether nothing at this depth is still actionable.
//
// A dependency GitHub has no database for stays `unavailable` forever; counting it as outstanding
// would make its depth permanently incomplete and the objective permanently unsatisfiable.
func (d DepthProgress) Complete() bool { return d.Settled >= d.Total }

// Completeness reports progress at every depth, shallowest first.
func (q *Queue) Completeness() ([]DepthProgress, error) {
	rows, err := q.Store.DB().Query(`
		SELECT depth,
		       COUNT(*),
		       SUM(status = ?),
		       SUM(status IN (?, ?)),
		       SUM(status = ?),
		       SUM(status = ?)
		FROM abom_frontier
		GROUP BY depth
		ORDER BY depth`,
		StatusDone, StatusDone, StatusUnavailable, StatusPending, StatusRunning)
	if err != nil {
		return nil, fmt.Errorf("reading completeness by depth: %w", err)
	}
	defer rows.Close()

	out := []DepthProgress{}
	for rows.Next() {
		var d DepthProgress
		if err := rows.Scan(&d.Depth, &d.Total, &d.Done, &d.Settled, &d.Pending,
			&d.Running); err != nil {
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
