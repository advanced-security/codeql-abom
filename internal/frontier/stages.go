package frontier

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The objective's stages, in the order the crawl claims from them.
const (
	// StageExpedited is work somebody asked for by name, or a push to a repository they own.
	StageExpedited = "expedited"
	// StageCurrent is work never analyzed at the depth being worked, and commits whose
	// declarations are not known.
	StageCurrent = "current"
	// StageExplore is work never analyzed, deeper than the depth being worked.
	StageExplore = "explore"
	// StageRefresh is references that moved since they were analyzed, the stalest first.
	StageRefresh = "refresh"
)

// Stages is the pending work split by the stage that claims it.
type Stages struct {
	// Next names the stage the next claim draws from, and is empty when nothing is claimable now.
	Next      string `json:"next"`
	Expedited int    `json:"expedited"`
	Current   int    `json:"current"`
	Explore   int    `json:"explore"`
	Refresh   int    `json:"refresh"`
	// Unreached counts pending rows for repositories the owned closure does not reach, which are
	// never claimed.
	Unreached int `json:"unreached"`
}

// stageOfRow names a pending row's stage, given the depth being worked as its one argument.
const stageOfRow = `CASE
	WHEN expedited_at IS NOT NULL THEN '` + StageExpedited + `'
	WHEN requeued_for IS 'moved' THEN '` + StageRefresh + `'
	WHEN requeued_for IS 'reobserve' OR depth <= ? THEN '` + StageCurrent + `'
	ELSE '` + StageExplore + `' END`

// Stages reports the pending work inside the closure by stage, and what the closure does not reach.
func (q *Queue) Stages() (Stages, error) {
	var out Stages
	progress, err := q.Completeness()
	if err != nil {
		return out, err
	}
	working, outstanding := ShallowestIncomplete(progress)
	if !outstanding {
		// No depth is open, so whatever is still never analyzed is stuck, at any depth.
		working = math.MaxInt32
	}
	inside, bound, err := q.bound()
	if err != nil {
		return out, err
	}

	rows, err := q.Store.DB().Query(`
		SELECT `+stageOfRow+`, COUNT(*) FROM abom_frontier
		WHERE status = ? AND `+inside+`
		GROUP BY 1`, append([]any{working, StatusPending}, bound...)...)
	if err != nil {
		return out, fmt.Errorf("reading the stages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var stage string
		var n int
		if err := rows.Scan(&stage, &n); err != nil {
			return out, fmt.Errorf("scanning a stage: %w", err)
		}
		switch stage {
		case StageExpedited:
			out.Expedited = n
		case StageCurrent:
			out.Current = n
		case StageExplore:
			out.Explore = n
		case StageRefresh:
			out.Refresh = n
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	if err := q.Store.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_frontier WHERE status = ? AND NOT (`+inside+`)`,
		append([]any{StatusPending}, bound...)...).Scan(&out.Unreached); err != nil {
		return out, fmt.Errorf("counting queued work the closure does not reach: %w", err)
	}

	now := q.now().Format(time.RFC3339Nano)
	err = q.Store.DB().QueryRow(`
		SELECT `+stageOfRow+` FROM abom_frontier
		WHERE status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?) AND `+inside+`
		ORDER BY `+store.FrontierClaimOrder+`
		LIMIT 1`, append([]any{working, StatusPending, now}, bound...)...).Scan(&out.Next)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("reading the next stage: %w", err)
	}
	return out, nil
}

// Inside reports whether a repository is inside the owned closure, in any case, or is nil when
// the queue is unbounded.
func (q *Queue) Inside() (func(repository string) bool, error) {
	if q.Closure == nil {
		return nil, nil
	}
	names, err := q.Closure()
	if err != nil {
		return nil, fmt.Errorf("reading the owned closure: %w", err)
	}
	reached := make(map[string]bool, len(names))
	for _, name := range names {
		reached[strings.ToLower(name)] = true
	}
	return func(repository string) bool { return reached[strings.ToLower(repository)] }, nil
}

// bound is the condition admitting a row inside the owned closure, and its argument. Without a
// closure every row is inside.
func (q *Queue) bound() (string, []any, error) {
	if q.Closure == nil {
		return "1", nil, nil
	}
	names, err := q.Closure()
	if err != nil {
		return "", nil, fmt.Errorf("reading the owned closure: %w", err)
	}
	inside, args := store.BoundTo(names).Admits("")
	return inside, args, nil
}
