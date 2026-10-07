package frontier

import (
	"database/sql"
	"testing"
)

// pushed is an owned repository analyzed at analyzedAt whose push a probe found moved: expedited,
// as the event processor leaves it.
func pushed(t *testing.T, q *Queue, key, analyzedAt string) Item {
	t.Helper()
	it := moved(t, q, key, 0, analyzedAt)
	if _, err := q.ExpediteRefs([]NodeRef{{NodeKey: it.NodeKey, Ref: it.Ref}}); err != nil {
		t.Fatalf("ExpediteRefs %s: %v", key, err)
	}
	return it
}

func reviewed(t *testing.T, q *Queue, it Item, moved bool) {
	t.Helper()
	if err := q.ReviewDependencies(it.Key(), moved); err != nil {
		t.Fatalf("ReviewDependencies %s: %v", it.NodeKey, err)
	}
}

func hintOf(t *testing.T, q *Queue, key string) sql.NullBool {
	t.Helper()
	var got sql.NullBool
	if err := q.Store.DB().QueryRow(
		`SELECT dependencies_moved FROM abom_frontier WHERE node_key = ?`, key).Scan(&got); err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	return got
}

// Pushes arrive faster than they are analyzed, and one whose `uses:` moved changes the graph's shape:
// it goes ahead of the pushes waiting with it, even ones that have waited longer.
func TestAPushWhoseActionsDependenciesMovedIsClaimedFirst(t *testing.T) {
	q, _ := newQueue(t)
	pushed(t, q, "a/stale", "2026-01-01T00:00:00Z")
	recent := pushed(t, q, "b/recent", "2026-06-01T00:00:00Z")
	reviewed(t, q, recent, true)

	if got := claimedKeys(t, q, 2); len(got) != 2 || got[0] != "b/recent" {
		t.Errorf("claimed %v, want b/recent first: its Actions dependencies moved", got)
	}
}

// Dependency review does not read a composite action's action.yml, so a comparison that saw nothing
// is no evidence nothing moved: it leaves a push where it was, never behind one nobody compared.
func TestAPushDependencyReviewSawNothingInKeepsItsPlace(t *testing.T) {
	q, _ := newQueue(t)
	stale := pushed(t, q, "a/stale", "2026-01-01T00:00:00Z")
	pushed(t, q, "b/recent", "2026-06-01T00:00:00Z")
	reviewed(t, q, stale, false)

	if got := claimedKeys(t, q, 2); len(got) != 2 || got[0] != "a/stale" {
		t.Errorf("claimed %v, want a/stale first, longest since analyzed", got)
	}
}

// Somebody naming a repository states an objective the crawl cannot infer; a push's hint orders
// pushes among themselves and nothing else.
func TestTheHintDoesNotJumpWorkAskedForByName(t *testing.T) {
	q, _ := newQueue(t)
	named := unanalyzed(t, q, "a/named", 2)
	if _, err := q.ExpediteRefs([]NodeRef{{NodeKey: named.NodeKey, Ref: named.Ref}}); err != nil {
		t.Fatal(err)
	}
	reviewed(t, q, pushed(t, q, "b/pushed", "2026-01-01T00:00:00Z"), true)

	if got := claimedKeys(t, q, 1); len(got) != 1 || got[0] != "a/named" {
		t.Errorf("claimed %v, want the named repository first", got)
	}
}

// The reading describes one pending spell. It survives a failed attempt or a release, goes when the
// row settles however it settles, and a later move starts with none.
func TestTheHintLastsUntilTheRowSettles(t *testing.T) {
	q, _ := newQueue(t)
	it := pushed(t, q, "a/pushed", "2026-01-01T00:00:00Z")
	reviewed(t, q, it, true)
	if got := claimedKeys(t, q, 1); len(got) != 1 {
		t.Fatalf("claimed %v", got)
	}
	if err := q.Fail(it.Key(), "exit status 128"); err != nil {
		t.Fatal(err)
	}
	if h := hintOf(t, q, "a/pushed"); !h.Valid || !h.Bool {
		t.Errorf("after a failure the hint is %+v, want it kept", h)
	}
	if err := q.Release(it.Key()); err != nil {
		t.Fatal(err)
	}
	if h := hintOf(t, q, "a/pushed"); !h.Valid || !h.Bool {
		t.Errorf("after a release the hint is %+v, want it kept", h)
	}

	for name, settle := range map[string]func(Key) error{
		"analyzed":    func(k Key) error { return q.Complete(k, Resolution{Ref: "v1", SHA: "new"}) },
		"unavailable": func(k Key) error { return q.Unavailable(k, "gone") },
		"nothing":     func(k Key) error { return q.Nothing(k, "v1", "new", "no workflows") },
	} {
		if err := q.MarkStale(it.Key(), "newer"); err != nil {
			t.Fatal(err)
		}
		reviewed(t, q, it, true)
		if err := settle(it.Key()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if h := hintOf(t, q, "a/pushed"); h.Valid {
			t.Errorf("%s: the hint is %+v after the row settled, want none", name, h)
		}
	}

	reviewed(t, q, it, true)
	if h := hintOf(t, q, "a/pushed"); h.Valid {
		t.Errorf("a settled row took a hint: %+v", h)
	}
	if err := q.MarkStale(it.Key(), "newest"); err != nil {
		t.Fatal(err)
	}
	if h := hintOf(t, q, "a/pushed"); h.Valid {
		t.Errorf("a new move kept the last one's hint: %+v", h)
	}
}

// A probe can find a reference moved that was never read at any commit. Analyzing it is the first
// stage's work, not a refresh: it holds its depth open until it is analyzed.
func TestAMoveOfARowNeverReadIsNotARefresh(t *testing.T) {
	q, _ := newQueue(t)
	it := unanalyzed(t, q, "a/unread", 1)
	if err := q.Unavailable(it.Key(), "the branch was deleted"); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkStale(it.Key(), "back"); err != nil {
		t.Fatal(err)
	}

	progress, err := q.Completeness()
	if err != nil {
		t.Fatal(err)
	}
	if depth, ok := ShallowestIncomplete(progress); !ok || depth != 1 {
		t.Errorf("progress = %+v, want depth 1 held open by a row never analyzed", progress)
	}
	stages, err := q.Stages()
	if err != nil {
		t.Fatal(err)
	}
	if stages.Refresh != 0 || stages.Current != 1 {
		t.Errorf("stages = %+v, want it in the current stage", stages)
	}
}
