package frontier

import (
	"testing"
	"time"
)

// `checked_at` is the signal that says whether freshness is working. Complete wrote it equal to
// `analyzed_at`, so `checked_at > analyzed_at` was not merely 0 across all 2,800 rows — it was
// structurally impossible, and the measurement read as if freshness always worked.
func TestCompletingAnAnalysisDoesNotCountAsProbingIt(t *testing.T) {
	q := probeQueue(t)
	enqueueOne(t, q, "actions/checkout", "v4")

	if err := q.Complete(Key{NodeKey: "actions/checkout", Ref: "v4"},
		Resolution{Ref: "v4", SHA: "cafe1234"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	analyzed, checked := freshness(t, q, "actions/checkout", "v4")
	if analyzed == "" {
		t.Fatal("Complete did not record when the analysis happened")
	}
	if checked != "" {
		t.Errorf("checked_at = %q after an analysis; nothing probed the reference", checked)
	}
}

// Probing is what `checked_at` records, and it has to be distinguishable from the analysis that
// preceded it or the freshness signal cannot be read.
func TestProbingARecordsThatItWasProbed(t *testing.T) {
	q := probeQueue(t)
	enqueueOne(t, q, "actions/checkout", "v4")
	if err := q.Complete(Key{NodeKey: "actions/checkout", Ref: "v4"},
		Resolution{Ref: "v4", SHA: "cafe1234"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	q.Now = func() time.Time { return time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC) }
	if err := q.Touch(Key{NodeKey: "actions/checkout", Ref: "v4"}); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	analyzed, checked := freshness(t, q, "actions/checkout", "v4")
	if checked == "" {
		t.Fatal("a probe recorded nothing")
	}
	if checked <= analyzed {
		t.Errorf("checked_at %q is not after analyzed_at %q, so a probe is invisible",
			checked, analyzed)
	}
}

// A row nothing has probed since it was analyzed is exactly the population freshness is for.
// With checked_at written at analysis time it was empty, and the TTL gate made it look satisfied.
func TestARowNeverProbedIsDueRegardlessOfWhenItWasAnalyzed(t *testing.T) {
	q := probeQueue(t)
	enqueueOne(t, q, "actions/checkout", "v4")
	if err := q.Complete(Key{NodeKey: "actions/checkout", Ref: "v4"},
		Resolution{Ref: "v4", SHA: "cafe1234"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Past the TTL since the analysis, and still never probed.
	q.Now = func() time.Time { return time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC) }
	due, err := q.DueForRecheck(24*time.Hour, 10)
	if err != nil {
		t.Fatalf("DueForRecheck: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("due = %d, want the row nothing has ever probed", len(due))
	}
}

// Probing something analyzed moments ago teaches nothing: the analysis already resolved the
// commit. Consulting only `checked_at` would put the whole corpus on a probe treadmill the first
// time it ran.
func TestARowAnalyzedMomentsAgoIsNotProbed(t *testing.T) {
	q := probeQueue(t)
	enqueueOne(t, q, "actions/checkout", "v4")
	if err := q.Complete(Key{NodeKey: "actions/checkout", Ref: "v4"},
		Resolution{Ref: "v4", SHA: "cafe1234"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	q.Now = func() time.Time { return time.Date(2026, 9, 7, 0, 0, 1, 0, time.UTC) }
	due, err := q.DueForRecheck(24*time.Hour, 10)
	if err != nil {
		t.Fatalf("DueForRecheck: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("due = %d one second after the analysis that resolved it", len(due))
	}
}

// Never-probed rows come first, so a backlog is worked off rather than the same valuable few
// being re-probed while the rest are never touched at all.
func TestNeverProbedRowsAreOfferedBeforeAlreadyProbedOnes(t *testing.T) {
	q := probeQueue(t)
	for _, k := range []string{"actions/valuable", "actions/humble"} {
		enqueueOne(t, q, k, "v1")
		if err := q.Complete(Key{NodeKey: k, Ref: "v1"},
			Resolution{Ref: "v1", SHA: "sha-" + k}); err != nil {
			t.Fatalf("Complete(%s): %v", k, err)
		}
	}
	// The valuable one has been probed; the humble one never has.
	if _, err := q.Store.DB().Exec(
		`UPDATE abom_frontier SET score = 100 WHERE node_key = 'actions/valuable'`); err != nil {
		t.Fatalf("scoring: %v", err)
	}
	q.Now = func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }
	if err := q.Touch(Key{NodeKey: "actions/valuable", Ref: "v1"}); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	q.Now = func() time.Time { return time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC) }
	due, err := q.DueForRecheck(24*time.Hour, 10)
	if err != nil {
		t.Fatalf("DueForRecheck: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("due = %d, want both", len(due))
	}
	if due[0].NodeKey != "actions/humble" {
		t.Errorf("first due is %s; the never-probed row must come before the probed one even "+
			"though the other scores higher", due[0].NodeKey)
	}
}

// And once probed, it is not due again until the TTL has passed — or every cycle would re-probe
// the whole corpus, which is the 800-of-1,997 treadmill this plan exists to avoid.
func TestAJustProbedRowIsNotDueAgain(t *testing.T) {
	q := probeQueue(t)
	enqueueOne(t, q, "actions/checkout", "v4")
	if err := q.Complete(Key{NodeKey: "actions/checkout", Ref: "v4"},
		Resolution{Ref: "v4", SHA: "cafe1234"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := q.Touch(Key{NodeKey: "actions/checkout", Ref: "v4"}); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	due, err := q.DueForRecheck(24*time.Hour, 10)
	if err != nil {
		t.Fatalf("DueForRecheck: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("due = %d immediately after a probe", len(due))
	}
}

// A SHA cannot move, so probing one is pure waste. The existing filter says this by comparing a
// ref name against a commit, which is true for every tag and false only when the ref is itself a
// SHA — it works, and nothing about it says so.
func TestAnImmutableReferenceIsNeverDueForAProbe(t *testing.T) {
	q := probeQueue(t)
	enqueueOne(t, q, "actions/checkout", "cafe1234")
	if err := q.Complete(Key{NodeKey: "actions/checkout", Ref: "cafe1234"},
		Resolution{Ref: "cafe1234", SHA: "cafe1234"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	q.Now = func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }
	due, err := q.DueForRecheck(24*time.Hour, 10)
	if err != nil {
		t.Fatalf("DueForRecheck: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("due = %d for a reference that cannot drift", len(due))
	}
}

func probeQueue(t *testing.T) *Queue {
	t.Helper()
	q, _ := newQueue(t)
	// Fixed, so "after the analysis" is a fact about the clock rather than about how fast the
	// test ran.
	q.Now = func() time.Time { return time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC) }
	return q
}

func enqueueOne(t *testing.T, q *Queue, key, ref string) {
	t.Helper()
	it := item(key, 1)
	it.Ref = ref
	if _, err := q.Enqueue([]Item{it}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
}

func freshness(t *testing.T, q *Queue, key, ref string) (analyzed, checked string) {
	t.Helper()
	var a, c any
	if err := q.Store.DB().QueryRow(
		`SELECT analyzed_at, checked_at FROM abom_frontier WHERE node_key = ? AND ref = ?`,
		key, ref).Scan(&a, &c); err != nil {
		t.Fatalf("reading freshness of %s@%s: %v", key, ref, err)
	}
	return asString(a), asString(c)
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
