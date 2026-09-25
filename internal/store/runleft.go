package store

import (
	"database/sql"
	"fmt"
	"math"
)

// RunLeft is what the start of a run under way has still to do, and how long that may take.
//
// A run started again keeps each member's state from the last start until it is reached, so a
// member is still to do when it waits, runs, or finished only before this start began. A member
// no run can build is never to do: counting it would leave a run that has done everything it can
// with something left.
type RunLeft struct {
	Members int `json:"members"`
	// Finished counts the members this start has run to an end, which the pace is taken from.
	Finished int `json:"finished"`
	// RemainingSeconds is their average times what is left. Absent before one of them has
	// finished: a run's pace is its own, and a forecast from other queries over other repositories
	// on another day is not it.
	RemainingSeconds *int64 `json:"remainingSeconds"`
}

// stillToDo reads a member m of run r; see RunLeft.
const stillToDo = `(r.state IN ('pending', 'running') AND m.status <> 'no_database' AND
	(m.status IN ('pending', 'running') OR m.finished_at IS NULL OR m.finished_at < r.started_at))`

// finishedThisStart is a member this start ran and saw to an end. One skipped without running
// keeps an earlier start's beginning, so its time is not this start's pace.
const finishedThisStart = `(r.state = 'running' AND m.started_at >= r.started_at
	AND m.finished_at IS NOT NULL AND m.status NOT IN ('pending', 'running'))`

// runLeftOf is what a run has left, nil for one that is not pending or running.
func (s *Store) runLeftOf(id int64) (*RunLeft, error) {
	var state string
	var left RunLeft
	var pace sql.NullFloat64
	err := s.db.QueryRow(`
		SELECT r.state,
		       COALESCE(SUM(CASE WHEN `+stillToDo+` THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN `+finishedThisStart+` THEN 1 ELSE 0 END), 0),
		       AVG(CASE WHEN `+finishedThisStart+`
		           THEN (julianday(m.finished_at) - julianday(m.started_at)) * 86400 END)
		FROM abom_analysis_run r
		LEFT JOIN abom_analysis_member m ON m.run_id = r.row_id
		WHERE r.row_id = ? GROUP BY r.row_id`, id).
		Scan(&state, &left.Members, &left.Finished, &pace)
	if err != nil {
		return nil, fmt.Errorf("reading what run %d has left: %w", id, err)
	}
	if state != RunPending && state != RunRunning {
		return nil, nil
	}
	if pace.Valid && left.Finished > 0 {
		remaining := int64(math.Round(pace.Float64 * float64(left.Members)))
		left.RemainingSeconds = &remaining
	}
	return &left, nil
}
