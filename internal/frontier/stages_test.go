package frontier

import (
	"testing"
	"time"
)

// unanalyzed queues key at depth, never analyzed.
func unanalyzed(t *testing.T, q *Queue, key string, depth int) Item {
	t.Helper()
	it := item(key, depth)
	it.Ref = "v1"
	if _, err := q.Enqueue([]Item{it}); err != nil {
		t.Fatalf("Enqueue %s: %v", key, err)
	}
	return it
}

// moved analyzes key at analyzedAt and then has a probe find its reference elsewhere, which is
// how the recheck, the poller and an owned push all leave it.
func moved(t *testing.T, q *Queue, key string, depth int, analyzedAt string) Item {
	t.Helper()
	it := unanalyzed(t, q, key, depth)
	if err := q.Complete(it.Key(), Resolution{Ref: "v1", SHA: "old"}); err != nil {
		t.Fatalf("Complete %s: %v", key, err)
	}
	if _, err := q.Store.DB().Exec(
		`UPDATE abom_frontier SET analyzed_at = ? WHERE node_key = ?`, analyzedAt, key); err != nil {
		t.Fatalf("dating %s: %v", key, err)
	}
	if err := q.MarkStale(it.Key(), "new"); err != nil {
		t.Fatalf("MarkStale %s: %v", key, err)
	}
	return it
}

func claimedKeys(t *testing.T, q *Queue, limit int) []string {
	t.Helper()
	claimed, err := q.Claim(limit)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	keys := make([]string, 0, len(claimed))
	for _, it := range claimed {
		keys = append(keys, it.NodeKey)
	}
	return keys
}

// Re-analyzing what moved matters less than analyzing what was never seen, so a moved reference
// waits for every depth to be explored, however shallow it sits.
func TestAReferenceThatMovedWaitsForEverythingNeverAnalyzed(t *testing.T) {
	q, _ := newQueue(t)
	moved(t, q, "a/moved", 1, "2026-01-01T00:00:00Z")
	unanalyzed(t, q, "b/deep", 3)

	if got := claimedKeys(t, q, 1); len(got) != 1 || got[0] != "b/deep" {
		t.Errorf("claimed %v first, want b/deep: it was never analyzed", got)
	}
	if got := claimedKeys(t, q, 1); len(got) != 1 || got[0] != "a/moved" {
		t.Errorf("claimed %v next, want the moved reference", got)
	}
}

// The third stage is the stalest live version, not the most used or the shallowest.
func TestTheStalestMovedReferenceIsRefreshedFirst(t *testing.T) {
	q, _ := newQueue(t)
	moved(t, q, "a/recent", 1, "2026-06-01T00:00:00Z")
	moved(t, q, "b/stale", 2, "2026-01-01T00:00:00Z")
	if _, err := q.Store.DB().Exec(
		`UPDATE abom_frontier SET score = 1000 WHERE node_key = 'a/recent'`); err != nil {
		t.Fatalf("scoring: %v", err)
	}

	if got := claimedKeys(t, q, 1); len(got) != 1 || got[0] != "b/stale" {
		t.Errorf("claimed %v, want b/stale, analyzed longest ago", got)
	}
}

// An operator naming one repository, or a push to one they own, states an objective the crawl
// cannot infer, so it goes ahead of every stage.
func TestExpeditedWorkOutranksEveryStage(t *testing.T) {
	q, _ := newQueue(t)
	it := moved(t, q, "a/pushed", 2, "2026-01-01T00:00:00Z")
	unanalyzed(t, q, "b/new", 1)
	if _, err := q.ExpediteRefs([]NodeRef{{NodeKey: it.NodeKey, Ref: it.Ref}}); err != nil {
		t.Fatalf("ExpediteRefs: %v", err)
	}

	if got := claimedKeys(t, q, 1); len(got) != 1 || got[0] != "a/pushed" {
		t.Errorf("claimed %v, want the expedited reference", got)
	}
}

// A commit whose declarations are not known is a gap in what is known, not a refresh: it goes with
// the first stage whatever its depth.
func TestACommitBeingReobservedGoesWithTheFirstStage(t *testing.T) {
	q, _ := newQueue(t)
	it := unanalyzed(t, q, "a/reobserved", 3)
	if err := q.Complete(it.Key(), Resolution{Ref: "v1", SHA: "c1"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := q.Store.DB().Exec(`UPDATE abom_frontier SET status = 'pending',
		requeued_for = 'reobserve' WHERE node_key = 'a/reobserved'`); err != nil {
		t.Fatalf("putting back: %v", err)
	}
	unanalyzed(t, q, "b/next", 2)

	if got := claimedKeys(t, q, 1); len(got) != 1 || got[0] != "a/reobserved" {
		t.Errorf("claimed %v, want the commit being re-observed", got)
	}
}

// What a row was requeued for describes one pending spell. Once it settles, however it settles, a
// later requeue for a new pack or a retry is never-analyzed work again, not a refresh.
func TestSettlingForgetsWhyARowWasRequeued(t *testing.T) {
	for name, settle := range map[string]func(*Queue, Key) error{
		"analyzed":    func(q *Queue, k Key) error { return q.Complete(k, Resolution{Ref: "v1", SHA: "new"}) },
		"unavailable": func(q *Queue, k Key) error { return q.Unavailable(k, "gone") },
		"nothing":     func(q *Queue, k Key) error { return q.Nothing(k, "v1", "new", "no workflows") },
	} {
		t.Run(name, func(t *testing.T) {
			q, _ := newQueue(t)
			it := moved(t, q, "a/moved", 1, "2026-01-01T00:00:00Z")
			if got := claimedKeys(t, q, 1); len(got) != 1 {
				t.Fatalf("claimed %v, want the moved reference", got)
			}
			if err := settle(q, it.Key()); err != nil {
				t.Fatalf("settling: %v", err)
			}
			if _, err := q.Store.DB().Exec(
				`UPDATE abom_frontier SET status = 'pending' WHERE node_key = 'a/moved'`); err != nil {
				t.Fatalf("requeueing: %v", err)
			}
			unanalyzed(t, q, "b/deep", 3)

			if got := claimedKeys(t, q, 1); len(got) != 1 || got[0] != "a/moved" {
				t.Errorf("claimed %v, want a/moved by its depth", got)
			}
		})
	}
}

// A claim handed back unworked is the same refresh it was.
func TestAReleasedRefreshIsStillARefresh(t *testing.T) {
	q, _ := newQueue(t)
	it := moved(t, q, "a/moved", 1, "2026-01-01T00:00:00Z")
	if got := claimedKeys(t, q, 1); len(got) != 1 {
		t.Fatalf("claimed %v, want the moved reference", got)
	}
	if err := q.Release(it.Key()); err != nil {
		t.Fatalf("Release: %v", err)
	}
	unanalyzed(t, q, "b/deep", 3)

	if got := claimedKeys(t, q, 1); len(got) != 1 || got[0] != "b/deep" {
		t.Errorf("claimed %v, want b/deep ahead of the released refresh", got)
	}
}

// A moved reference that keeps failing is stuck, and counted there only: counted twice, a depth's
// parts would add up to more than the depth.
func TestAStuckRefreshIsCountedOnceAsStuck(t *testing.T) {
	q, _ := newQueue(t)
	moved(t, q, "a/moved", 1, "2026-01-01T00:00:00Z")
	if _, err := q.Store.DB().Exec(`UPDATE abom_frontier SET attempts = ?
		WHERE node_key = 'a/moved'`, q.stuckAfter()); err != nil {
		t.Fatal(err)
	}

	progress, err := q.Completeness()
	if err != nil {
		t.Fatalf("Completeness: %v", err)
	}
	if len(progress) != 1 || progress[0].Stuck != 1 || progress[0].Refresh != 0 {
		t.Errorf("progress = %+v, want it stuck and not also being refreshed", progress)
	}
}

// A failed attempt does not change why the row is pending.
func TestAFailedRefreshIsStillARefresh(t *testing.T) {
	q, _ := newQueue(t)
	q.Now = func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }
	it := moved(t, q, "a/moved", 1, "2026-01-01T00:00:00Z")
	if got := claimedKeys(t, q, 1); len(got) != 1 {
		t.Fatalf("claimed %v, want the moved reference", got)
	}
	if err := q.Fail(it.Key(), "exit status 128"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	q.Now = func() time.Time { return time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC) }
	unanalyzed(t, q, "b/deep", 3)

	if got := claimedKeys(t, q, 1); len(got) != 1 || got[0] != "b/deep" {
		t.Errorf("claimed %v, want b/deep ahead of the retried refresh", got)
	}
}

// bounded keeps the queue inside the named repositories, as the owned closure does.
func bounded(q *Queue, names ...string) {
	q.Closure = func() ([]string, error) { return names, nil }
}

// A repository nothing owned reaches is somebody else's supply chain. Its rows stay on record,
// and the crawl spends nothing on them, not even when somebody asked.
func TestWorkOutsideTheOwnedClosureIsNeverClaimed(t *testing.T) {
	q, _ := newQueue(t)
	unanalyzed(t, q, "octo/mine", 2)
	theirs := unanalyzed(t, q, "other/theirs", 1)
	if _, err := q.ExpediteRefs([]NodeRef{{NodeKey: theirs.NodeKey, Ref: theirs.Ref}}); err != nil {
		t.Fatalf("ExpediteRefs: %v", err)
	}
	bounded(q, "Octo/Mine")

	if got := claimedKeys(t, q, 10); len(got) != 1 || got[0] != "octo/mine" {
		t.Errorf("claimed %v, want only octo/mine", got)
	}
}

// A row the crawl never claims must not hold its depth open, keep the queue from draining, or wake
// the loop to claim it: any of the three and the crawl spins or freshness never runs.
func TestWorkOutsideTheClosureNeitherHoldsADepthOpenNorWakesTheLoop(t *testing.T) {
	q, _ := newQueue(t)
	mine := unanalyzed(t, q, "octo/mine", 2)
	if err := q.Complete(mine.Key(), Resolution{Ref: "v1", SHA: "c1"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	unanalyzed(t, q, "other/theirs", 1)
	bounded(q, "octo/mine")

	progress, err := q.Completeness()
	if err != nil {
		t.Fatalf("Completeness: %v", err)
	}
	if depth, ok := ShallowestIncomplete(progress); ok {
		t.Errorf("depth %d reported incomplete by work outside the closure: %+v", depth, progress)
	}
	if _, pending, err := q.NextAttemptAt(); err != nil || pending {
		t.Errorf("NextAttemptAt pending = %v, %v; want no claimable work", pending, err)
	}
	if drained, err := q.Drained(); err != nil || !drained {
		t.Errorf("Drained = %v, %v; want drained", drained, err)
	}
}

// Probing is spent where the crawl would act on the answer.
func TestOnlyRowsInsideTheClosureAreProbed(t *testing.T) {
	q, _ := newQueue(t)
	for _, key := range []string{"octo/mine", "other/theirs"} {
		it := unanalyzed(t, q, key, 1)
		if err := q.Complete(it.Key(), Resolution{Ref: "v1", SHA: "c1"}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
	bounded(q, "octo/mine")
	q.Now = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }

	due, err := q.DueForRecheck(time.Hour, 10)
	if err != nil {
		t.Fatalf("DueForRecheck: %v", err)
	}
	if len(due) != 1 || due[0].NodeKey != "octo/mine" {
		t.Errorf("due = %+v, want only octo/mine", due)
	}
}

// A moved reference was analyzed once: it is the third stage's work, and it does not reopen the
// depth it sits at, or freshness would wait on itself.
func TestAMovedReferenceDoesNotReopenItsDepth(t *testing.T) {
	q, _ := newQueue(t)
	moved(t, q, "a/moved", 1, "2026-01-01T00:00:00Z")

	progress, err := q.Completeness()
	if err != nil {
		t.Fatalf("Completeness: %v", err)
	}
	if depth, ok := ShallowestIncomplete(progress); ok {
		t.Errorf("depth %d reported incomplete by a moved reference: %+v", depth, progress)
	}
	if len(progress) != 1 || progress[0].Refresh != 1 {
		t.Errorf("progress = %+v, want one row being refreshed at depth 1", progress)
	}
}

// The objective names the stage the next claim draws from and how much each stage holds, so a
// reader is told what the crawl is doing rather than left to infer it from a depth.
func TestTheStagesSayWhatTheNextClaimTakes(t *testing.T) {
	q, _ := newQueue(t)
	moved(t, q, "a/moved", 1, "2026-01-01T00:00:00Z")
	unanalyzed(t, q, "b/current", 2)
	unanalyzed(t, q, "c/deeper", 3)
	unanalyzed(t, q, "other/theirs", 1)
	bounded(q, "a/moved", "b/current", "c/deeper")

	got, err := q.Stages()
	if err != nil {
		t.Fatalf("Stages: %v", err)
	}
	want := Stages{Next: StageCurrent, Current: 1, Explore: 1, Refresh: 1, Unreached: 1}
	if got != want {
		t.Errorf("Stages = %+v, want %+v", got, want)
	}

	for range 2 {
		if _, err := q.Claim(1); err != nil {
			t.Fatalf("Claim: %v", err)
		}
	}
	if got, err = q.Stages(); err != nil || got.Next != StageRefresh {
		t.Errorf("Stages = %+v, %v; want the refresh next once nothing unanalyzed is left", got, err)
	}
}

// The closure's names arrive in whatever case the graph holds; a repository is one repository
// however its name is cased.
func TestInsideAdmitsTheClosureInAnyCase(t *testing.T) {
	q, _ := newQueue(t)
	if inside, err := q.Inside(); err != nil || inside != nil {
		t.Errorf("an unbounded queue reported a bound")
	}
	bounded(q, "Octo/Mine")
	inside, err := q.Inside()
	if err != nil {
		t.Fatalf("Inside: %v", err)
	}
	if !inside("octo/mine") || inside("other/theirs") {
		t.Errorf("inside admitted the wrong repositories")
	}
}
