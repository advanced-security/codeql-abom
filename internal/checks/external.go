package checks

import (
	"context"
	"maps"
	"slices"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// refBatch is how many references one query asks about, as many as GitHub answers in one.
const refBatch = 100

// The strata a sampled baseline is drawn within: a stale owned reference is a missed event, a stale
// external one a probe or poll that has not come round.
const (
	stratumOwned    = "owned"
	stratumExternal = "external"
)

// answer is what GitHub said of one question; failed is the error of a query it refused as a whole.
type answer struct {
	catalog.RefAnswer
	failed string
}

func (a answer) read() bool { return a.Commit != "" || a.Missing }

// unread names why an answer says nothing, without the subject, so a run's skips stay countable.
func (a answer) unread() string {
	if a.failed != "" {
		return "unread: the query failed: " + a.failed
	}
	return "unread: GitHub did not say where it points"
}

// resolve asks GitHub about each question once, a hundred to a query, charging the checker's GraphQL
// pool. A query that failed leaves its questions unread, never answered.
func (c *Checker) resolve(ctx context.Context, qs []catalog.RefQuestion) (map[catalog.RefQuestion]answer, error) {
	out := map[catalog.RefQuestion]answer{}
	seen := map[catalog.RefQuestion]bool{}
	var unique []catalog.RefQuestion
	for _, q := range qs {
		if !seen[q] {
			seen[q] = true
			unique = append(unique, q)
		}
	}
	for start := 0; start < len(unique); start += refBatch {
		batch := unique[start:min(start+refBatch, len(unique))]
		if err := c.GraphQL.Wait(ctx); err != nil {
			return nil, err
		}
		got, err := c.GitHub.ResolveRefs(ctx, batch)
		c.GraphQL.Spend(got.Requests, got.Cost)
		c.GraphQL.Observe(got.Reading)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		for i, q := range batch {
			switch {
			case err != nil:
				out[q] = answer{failed: err.Error()}
			case i < len(got.Answers):
				out[q] = answer{RefAnswer: got.Answers[i]}
			default:
				out[q] = answer{failed: "GitHub answered fewer questions than it was asked"}
			}
		}
	}
	return out, nil
}

// order lists every drawn index once, in the order the parts read them.
func (d draw) order() []int {
	seen := map[int]bool{}
	var out []int
	add := func(idx []int) {
		for _, i := range idx {
			if !seen[i] {
				seen[i] = true
				out = append(out, i)
			}
		}
	}
	add(d.reread)
	add(d.recent)
	for _, stratum := range slices.Sorted(maps.Keys(d.random)) {
		add(d.random[stratum])
	}
	return out
}

// openAmong splits a check's open subjects into those a candidate still claims, which are read
// again, and those nothing claims any more.
func openAmong(open []store.CheckSubject, cands []candidate) (map[string]bool, []store.CheckSubject) {
	keys := map[string]bool{}
	for _, c := range cands {
		keys[c.key] = true
	}
	claimed := map[string]bool{}
	var gone []store.CheckSubject
	for _, o := range open {
		if keys[o.Subject] {
			claimed[o.Subject] = true
		} else {
			gone = append(gone, o)
		}
	}
	return claimed, gone
}

// withdrawn settles disagreements about claims ABOM no longer makes: with nothing claimed there is
// nothing to disagree with, and leaving them open would keep them for ever.
func withdrawn(p *store.CheckPart, gone []store.CheckSubject, why string) {
	if len(gone) == 0 {
		return
	}
	p.Agreed = append(p.Agreed, gone...)
	p.Skipped = append(p.Skipped, store.CheckSkip{Reason: why, Count: len(gone)})
}

// bounded states what a random part's sample rules out, which a part not drawn at random cannot.
func bounded(p *store.CheckPart) {
	if p.Compared > 0 {
		b := upperBound(len(p.Disagreements), p.Compared)
		p.Bound = &b
	}
}

// queued says the queue holds a row for work, so its next analysis resolves the reference again.
func queued(status string) bool {
	return status == frontier.StatusPending || status == frontier.StatusRunning
}

// classOf is the rule: a move the queue already holds for work is staleness the work will clear;
// one it holds settled is a move nothing noticed.
func classOf(status string) string {
	if queued(status) {
		return "staleness"
	}
	return "unnoticed"
}

// held says how the queue holds a row, for a reader deciding which kind of lag it is.
func held(q store.QueueClaim) string {
	switch {
	case queued(q.Status):
		return "queued to be read again"
	case q.Confirmed != "":
		return q.Status + ", last found unmoved " + q.Confirmed
	case q.Changed != "":
		return q.Status + ", analyzed " + q.Changed
	}
	return q.Status
}
