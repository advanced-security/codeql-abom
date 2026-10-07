package events

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// reviewer answers dependency comparisons from canned readings and records each one asked.
type reviewer struct {
	readings map[string]catalog.DependencyComparison
	errs     map[string]error
	asked    []string
}

func (r *reviewer) compare(_ context.Context, repository, base, head string) (catalog.DependencyComparison, error) {
	key := repository + " " + base + "..." + head
	r.asked = append(r.asked, key)
	if err := r.errs[key]; err != nil {
		return catalog.DependencyComparison{}, err
	}
	return r.readings[key], nil
}

func reviewing(f *fixture) (*Processor, *reviewer) {
	r := &reviewer{readings: map[string]catalog.DependencyComparison{}, errs: map[string]error{}}
	p := f.processor()
	p.Dependencies = r.compare
	return p, r
}

var checkoutMoved = catalog.DependencyComparison{Changes: []catalog.DependencyChange{
	{ChangeType: "removed", Manifest: ".github/workflows/ci.yml", Ecosystem: "actions",
		Name: "actions/checkout", Version: "5.*.*"},
	{ChangeType: "added", Manifest: ".github/workflows/ci.yml", Ecosystem: "actions",
		Name: "actions/checkout", Version: "4.*.*"},
}}

func hint(t *testing.T, f *fixture, key, ref string) sql.NullBool {
	t.Helper()
	var got sql.NullBool
	if err := f.store.DB().QueryRow(`SELECT dependencies_moved FROM abom_frontier
		WHERE node_key = ? AND ref = ?`, key, ref).Scan(&got); err != nil {
		t.Fatalf("reading %s@%s: %v", key, ref, err)
	}
	return got
}

func analyzedAt(t *testing.T, f *fixture, key, at string) {
	t.Helper()
	if _, err := f.store.DB().Exec(`UPDATE abom_frontier SET analyzed_at = ? WHERE node_key = ?`,
		at, key); err != nil {
		t.Fatal(err)
	}
}

// Two pushes wait; the one whose workflow's `uses:` moved changes the graph's shape and is claimed
// first, though the other has waited longer since its analysis.
func TestAPushWhoseUsesMovedIsComparedAndClaimedAheadOfOtherPushes(t *testing.T) {
	f := newFixture(t)
	f.owned("acme/stale", "main", shaA)
	analyzedAt(t, f, "acme/stale", "2026-01-01T00:00:00Z")
	analyzedAt(t, f, "acme/service", "2026-06-01T00:00:00Z")
	f.answers["acme/stale@"] = shaB
	f.answers["acme/service@"] = shaB
	p, r := reviewing(f)
	r.readings["acme/service "+shaA+"..."+shaB] = checkoutMoved

	f.deliver("d-1", "push", pushPayload("acme/stale", "refs/heads/main", shaA, shaB))
	f.deliver("d-2", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	drainWith(t, p)

	if d := f.settled("d-2", store.EventDone, ReasonRequeued); !strings.Contains(d.Detail, "actions/checkout") ||
		!strings.Contains(d.Detail, "ahead of other pushes") {
		t.Errorf("detail %q does not say what moved and what that does", d.Detail)
	}
	if d := f.settled("d-1", store.EventDone, ReasonRequeued); !strings.Contains(d.Detail, "saw no") {
		t.Errorf("detail %q does not say the comparison saw nothing", d.Detail)
	}
	want := []string{"acme/stale " + shaA + "..." + shaB, "acme/service " + shaA + "..." + shaB}
	if !slices.Equal(r.asked, want) {
		t.Errorf("compared %v, want %v", r.asked, want)
	}
	if h := hint(t, f, "acme/service", ""); !h.Valid || !h.Bool {
		t.Errorf("acme/service hint = %+v, want moved", h)
	}
	claimed, err := f.queue.Claim(1)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].NodeKey != "acme/service" {
		t.Errorf("claimed %+v, want acme/service first", claimed)
	}
}

// The probe decides where the branch is; the payload's commits decide nothing, the comparison
// included: it runs from the commit the graph describes to the one the probe found.
func TestTheComparisonRunsFromTheAnalyzedCommitToTheProbedOne(t *testing.T) {
	f := newFixture(t)
	f.answers["acme/service@"] = shaC
	p, r := reviewing(f)

	f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", forged, forged))
	drainWith(t, p)

	if want := []string{"acme/service " + shaA + "..." + shaC}; !slices.Equal(r.asked, want) {
		t.Errorf("compared %v, want %v", r.asked, want)
	}
}

// A comparison GitHub refused, or one it could not complete and that names no move, is no reading:
// the push is requeued and waits with the others, and the delivery says why.
func TestAComparisonThatCannotBeReadChangesNothing(t *testing.T) {
	for name, set := range map[string]func(*reviewer){
		"refused": func(r *reviewer) {
			r.errs["acme/service "+shaA+"..."+shaB] = errors.New("403 Dependency review is not supported")
		},
		"incomplete": func(r *reviewer) {
			r.readings["acme/service "+shaA+"..."+shaB] = catalog.DependencyComparison{
				Warnings: "No snapshots were found for the head SHA"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.answers["acme/service@"] = shaB
			p, r := reviewing(f)
			set(r)

			f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
			drainWith(t, p)

			d := f.settled("d-1", store.EventDone, ReasonRequeued)
			if !strings.Contains(d.Detail, "waits with the others") {
				t.Errorf("detail %q does not say the push is ordered as any other", d.Detail)
			}
			if h := hint(t, f, "acme/service", ""); h.Valid {
				t.Errorf("hint = %+v, want none", h)
			}
			if row := f.row("acme/service", ""); !row.Expedited {
				t.Errorf("row = %+v, want it expedited all the same", row)
			}
		})
	}

	t.Run("an incomplete comparison that names a move still says it", func(t *testing.T) {
		f := newFixture(t)
		f.answers["acme/service@"] = shaB
		p, r := reviewing(f)
		partial := checkoutMoved
		partial.Warnings = "No snapshots were found for the head SHA"
		r.readings["acme/service "+shaA+"..."+shaB] = partial

		f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
		drainWith(t, p)

		if h := hint(t, f, "acme/service", ""); !h.Valid || !h.Bool {
			t.Errorf("hint = %+v, want moved", h)
		}
	})
}

// Nothing is compared where nothing moved, or where no commit was ever read to compare from.
func TestOnlyAMoveFromACommitReadIsCompared(t *testing.T) {
	t.Run("unchanged", func(t *testing.T) {
		f := newFixture(t)
		f.answers["acme/service@"] = shaA
		p, r := reviewing(f)
		f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaA))
		drainWith(t, p)
		if len(r.asked) != 0 {
			t.Errorf("compared %v for a branch that did not move", r.asked)
		}
	})
	t.Run("never read", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.store.DB().Exec(`UPDATE abom_frontier SET status = 'nothing', resolved_sha = NULL
			WHERE node_key = 'acme/service'`); err != nil {
			t.Fatal(err)
		}
		f.answers["acme/service@"] = shaB
		p, r := reviewing(f)
		f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", zeroSHA, shaB))
		drainWith(t, p)
		f.settled("d-1", store.EventDone, ReasonRequeued)
		if len(r.asked) != 0 {
			t.Errorf("compared %v from no commit", r.asked)
		}
	})
}

// Every row a push moved from one analyzed commit shares one comparison: one request per push.
func TestOnePushIsOneComparison(t *testing.T) {
	f := newFixture(t)
	f.analyzed("acme/service", "main", 1, shaA)
	if _, err := f.queue.Enqueue([]frontier.Item{{NodeKey: "acme/service/lint", Ref: "main",
		Owner: "acme", Repo: "service", Subpath: "lint", Depth: 2}}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := f.queue.Complete(frontier.Key{NodeKey: "acme/service/lint", Ref: "main"},
		frontier.Resolution{Ref: "main", SHA: shaA}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	f.answers["acme/service@"] = shaB
	f.answers["acme/service@main"] = shaB
	p, r := reviewing(f)
	r.readings["acme/service "+shaA+"..."+shaB] = checkoutMoved

	f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	drainWith(t, p)

	if len(r.asked) != 1 {
		t.Errorf("compared %v, want one comparison for the push", r.asked)
	}
	for _, row := range [][2]string{{"acme/service", ""}, {"acme/service", "main"}, {"acme/service/lint", "main"}} {
		if h := hint(t, f, row[0], row[1]); !h.Valid || !h.Bool {
			t.Errorf("%s@%s hint = %+v, want moved", row[0], row[1], h)
		}
	}
}

// A later push compares the analyzed commit with the newest one, so a move reverted before the
// analysis ran no longer jumps the queue, and a comparison that cannot be read keeps what was said.
func TestALaterPushComparesAgainFromTheAnalyzedCommit(t *testing.T) {
	f := newFixture(t)
	f.answers["acme/service@"] = shaB
	p, r := reviewing(f)
	r.readings["acme/service "+shaA+"..."+shaB] = checkoutMoved
	f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	drainWith(t, p)

	f.answers["acme/service@"] = shaC
	r.errs["acme/service "+shaA+"..."+shaC] = errors.New("HTTP 502")
	f.deliver("d-2", "push", pushPayload("acme/service", "refs/heads/main", shaB, shaC))
	drainWith(t, p)
	if h := hint(t, f, "acme/service", ""); !h.Valid || !h.Bool {
		t.Errorf("after an unreadable comparison the hint is %+v, want the last reading kept", h)
	}

	delete(r.errs, "acme/service "+shaA+"..."+shaC)
	f.deliver("d-3", "push", pushPayload("acme/service", "refs/heads/main", shaB, shaC))
	drainWith(t, p)
	if want := "acme/service " + shaA + "..." + shaC; len(r.asked) == 0 || r.asked[len(r.asked)-1] != want {
		t.Errorf("compared %v last, want %s", r.asked, want)
	}
	if h := hint(t, f, "acme/service", ""); !h.Valid || h.Bool {
		t.Errorf("hint = %+v, want nothing seen moving now", h)
	}
}

// A reference marked gone that answers again at the commit it was analyzed at is back, and between
// a commit and itself there is nothing to compare.
func TestAReferenceBackAtItsAnalyzedCommitIsNotCompared(t *testing.T) {
	f := newFixture(t)
	if err := f.queue.Unavailable(frontier.Key{NodeKey: "acme/service"}, "reference gone"); err != nil {
		t.Fatal(err)
	}
	f.answers["acme/service@"] = shaA
	p, r := reviewing(f)

	f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", zeroSHA, shaA))
	drainWith(t, p)

	f.settled("d-1", store.EventDone, ReasonBack)
	if len(r.asked) != 0 {
		t.Errorf("compared %v, a commit with itself", r.asked)
	}
}

// The processor follows deliveries one at a time, and a comparison is only a hint: GitHub slow to
// answer, or the event follow-up pool holding the request back, must not hold this push up or
// every push behind it.
func TestAComparisonThatDoesNotAnswerDoesNotHoldThePushUp(t *testing.T) {
	f := newFixture(t)
	f.answers["acme/service@"] = shaB
	p := f.processor()
	p.ReviewTimeout = 20 * time.Millisecond
	p.Dependencies = func(ctx context.Context, _, _, _ string) (catalog.DependencyComparison, error) {
		<-ctx.Done()
		return catalog.DependencyComparison{}, ctx.Err()
	}
	f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := p.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	d := f.settled("d-1", store.EventDone, ReasonRequeued)
	if !strings.Contains(d.Detail, "could not be read") {
		t.Errorf("detail %q does not say the comparison went unread", d.Detail)
	}
	if row := f.row("acme/service", ""); !row.Expedited {
		t.Errorf("row = %+v, want it expedited all the same", row)
	}
}
