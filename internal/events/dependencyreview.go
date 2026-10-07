package events

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DefaultReviewTimeout is how long a push waits for dependency review: GitHub answers in about a
// second, and a hint that takes longer is not worth holding every later delivery for.
const DefaultReviewTimeout = 10 * time.Second

func (p *Processor) reviewTimeout() time.Duration {
	if p.ReviewTimeout <= 0 {
		return DefaultReviewTimeout
	}
	return p.ReviewTimeout
}

// review is what dependency review said of one push, as the delivery reports it.
type review struct {
	// read says the comparison was read: moved is then whether a direct Actions dependency moved.
	read, moved bool
	detail      string
}

// reviewDependencies compares the commit a row was analyzed at with the one the probe found, once
// per pair in a delivery.
//
// Only a move it names counts. Dependency review reads workflows and not a composite action's
// action.yml, so a comparison that names nothing is no evidence that nothing moved, and a push it
// says nothing about waits with the others rather than behind them.
func (p *Processor) reviewDependencies(
	ctx context.Context, repository, base, head string, reviewed map[string]review,
) (review, error) {
	if p.Dependencies == nil || base == "" || strings.EqualFold(base, head) {
		return review{}, nil
	}
	asked := repository + "@" + base + "..." + head
	if r, ok := reviewed[asked]; ok {
		return r, nil
	}
	asking, cancel := context.WithTimeout(ctx, p.reviewTimeout())
	got, err := p.Dependencies(asking, repository, base, head)
	cancel()
	if err != nil && ctx.Err() != nil {
		return review{}, ctx.Err()
	}
	var r review
	moved := got.Actions()
	switch {
	case err != nil:
		r.detail = fmt.Sprintf("; dependency review could not be read (%v), so this push waits with the others", err)
	case len(moved) > 0:
		names := make([]string, 0, len(moved))
		for _, c := range moved {
			names = append(names, fmt.Sprintf("%s %s %s", c.Name, c.Version, c.ChangeType))
		}
		r = review{read: true, moved: true, detail: fmt.Sprintf(
			"; dependency review: %d direct Actions dependency changes (%s), so it is claimed ahead of other pushes",
			len(moved), sample(names, 4))}
	case got.Warnings != "":
		r.detail = fmt.Sprintf("; dependency review was incomplete (%s), so this push waits with the others",
			got.Warnings)
	default:
		r = review{read: true, detail: "; dependency review saw no direct Actions dependency move, " +
			"which it cannot see in an action.yml, so this push waits with the others"}
	}
	reviewed[asked] = r
	return r, nil
}
