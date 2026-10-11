package frontier

import (
	"slices"
	"testing"
	"time"
)

// heldQueue is one owned repository's default-branch row, pending after a push, on a clock a test moves.
func heldQueue(t *testing.T) (*Queue, Key, *time.Time) {
	t.Helper()
	q, _ := newQueue(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	q.Now = func() time.Time { return now }
	if _, err := q.Enqueue([]Item{{NodeKey: "octo/app", Owner: "octo", Repo: "app", Depth: 0}}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return q, Key{NodeKey: "octo/app"}, &now
}

func claimed(t *testing.T, q *Queue) []string {
	t.Helper()
	items, err := q.Claim(10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	var out []string
	for _, it := range items {
		out = append(out, it.NodeKey)
	}
	return out
}

func held(t *testing.T, q *Queue, k Key) string {
	t.Helper()
	var commit *string
	if err := sqliteDB(t, q).QueryRow(`SELECT awaiting_code_scanning FROM abom_frontier
		WHERE node_key = ? AND ref = ?`, k.NodeKey, k.Ref).Scan(&commit); err != nil {
		t.Fatalf("reading the hold on %s: %v", k.NodeKey, err)
	}
	if commit == nil {
		return ""
	}
	return *commit
}

// Analyzing a push before code scanning finishes builds what code scanning is about to supply, so
// a held row is not claimed until code scanning's analysis of its commit is reported.
func TestARowHeldForCodeScanningIsClaimedOnlyOnceItsCommitIsAnalyzed(t *testing.T) {
	q, k, _ := heldQueue(t)
	ok, err := q.AwaitCodeScanning(k, "C2", time.Date(2026, 10, 7, 12, 10, 0, 0, time.UTC))
	if err != nil || !ok {
		t.Fatalf("AwaitCodeScanning = %v, %v; want the pending row held", ok, err)
	}
	if got := claimed(t, q); len(got) != 0 {
		t.Fatalf("claimed %v while it was held", got)
	}
	next, pending, err := q.NextAttemptAt()
	if err != nil || !pending || !next.Equal(time.Date(2026, 10, 7, 12, 10, 0, 0, time.UTC)) {
		t.Errorf("the queue next has work at %v (%v, %v), want the ceiling", next, pending, err)
	}

	released, err := q.CodeScanningAnalyzed("octo/app", "c1")
	if err != nil || len(released) != 0 {
		t.Errorf("an analysis of another commit released %v (%v), want nothing", released, err)
	}
	released, err = q.CodeScanningAnalyzed("Octo/App", "c2")
	if err != nil || !slices.Equal(released, []Key{k}) {
		t.Fatalf("the analysis of its commit released %v (%v), want %v", released, err, k)
	}
	if got := claimed(t, q); !slices.Equal(got, []string{"octo/app"}) {
		t.Errorf("claimed %v once released, want octo/app", got)
	}
}

// The wait trades latency for a build, and only up to a ceiling: after it the row is claimed and
// built like any other, and stops reading as held.
func TestARowHeldPastItsCeilingIsClaimedAndNoLongerHeld(t *testing.T) {
	q, k, now := heldQueue(t)
	if _, err := q.AwaitCodeScanning(k, "c2", now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(11 * time.Minute)

	if got := claimed(t, q); !slices.Equal(got, []string{"octo/app"}) {
		t.Fatalf("claimed %v past the ceiling, want octo/app", got)
	}
	if got := held(t, q, k); got != "" {
		t.Errorf("a claimed row is still held for %q", got)
	}
}

// Only work waiting to be claimed can wait for code scanning; a settled row has nothing to wait for.
func TestOnlyAPendingRowIsHeld(t *testing.T) {
	q, k, _ := heldQueue(t)
	if err := q.Complete(k, Resolution{Ref: HeadRef, SHA: "c1"}); err != nil {
		t.Fatal(err)
	}
	ok, err := q.AwaitCodeScanning(k, "c2", time.Date(2026, 10, 7, 12, 10, 0, 0, time.UTC))
	if err != nil || ok {
		t.Errorf("AwaitCodeScanning on a done row = %v, %v; want nothing held", ok, err)
	}
	if got := held(t, q, k); got != "" {
		t.Errorf("a done row is held for %q", got)
	}
}

// Code scanning switched off reports nothing more, so what waited on it is released at once
// rather than at the ceiling.
func TestEveryHoldOfARepositoryCanBeReleased(t *testing.T) {
	q, k, _ := heldQueue(t)
	if _, err := q.AwaitCodeScanning(k, "c2", time.Date(2026, 10, 7, 12, 10, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	released, err := q.ReleaseCodeScanningHolds("octo/app")
	if err != nil || !slices.Equal(released, []Key{k}) {
		t.Fatalf("released %v (%v), want %v", released, err, k)
	}
	if got := held(t, q, k); got != "" {
		t.Errorf("the row is still held for %q", got)
	}
	if got := claimed(t, q); !slices.Equal(got, []string{"octo/app"}) {
		t.Errorf("claimed %v once released, want octo/app", got)
	}
}

// A held row waits on code scanning, not on the order, so it is its own stage and not counted
// among the work asked for by name that goes first.
func TestAHeldRowIsItsOwnStage(t *testing.T) {
	q, k, _ := heldQueue(t)
	if _, err := q.ExpediteRefs([]NodeRef{{NodeKey: k.NodeKey}}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AwaitCodeScanning(k, "c2", time.Date(2026, 10, 7, 12, 10, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	stages, err := q.Stages()
	if err != nil {
		t.Fatalf("Stages: %v", err)
	}
	if stages.Waiting != 1 || stages.Expedited != 0 || stages.Next != "" {
		t.Errorf("stages = %+v, want the row waiting, nothing expedited and nothing claimable", stages)
	}
}

// Asking for a repository by name states an objective the crawl cannot infer, so it is claimed now
// rather than after code scanning; a retry's backoff is not a hold, and stays.
func TestARepositoryAskedForByNameIsNoLongerHeld(t *testing.T) {
	q, k, _ := heldQueue(t)
	if _, err := q.AwaitCodeScanning(k, "c2", time.Date(2026, 10, 7, 12, 10, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue([]Item{{NodeKey: "octo/lib", Owner: "octo", Repo: "lib", Depth: 0}}); err != nil {
		t.Fatal(err)
	}
	if err := q.Fail(Key{NodeKey: "octo/lib"}, "clone failed"); err != nil {
		t.Fatal(err)
	}

	if _, err := q.Expedite("octo/app", "octo/lib"); err != nil {
		t.Fatalf("Expedite: %v", err)
	}
	if got := held(t, q, k); got != "" {
		t.Errorf("a repository asked for by name is still held for %q", got)
	}
	if got := claimed(t, q); !slices.Equal(got, []string{"octo/app"}) {
		t.Errorf("claimed %v, want octo/app now and octo/lib after its backoff", got)
	}
}
