package frontier

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func newQueue(t *testing.T) (*Queue, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return &Queue{Store: s, MaxDepth: 5}, s
}

// item mirrors what SeedFrontier produces: a key, and the account and repository it addresses.
// A frontier item without those cannot be fetched at all, so a fixture lacking them would be
// testing something the crawler never sees.
func item(key string, depth int) Item {
	owner, repo := "octo-org", key
	if o, r, found := strings.Cut(key, "/"); found {
		owner, repo = o, r
	}
	return Item{NodeKey: key, Owner: owner, Repo: repo, Depth: depth}
}

func TestEnqueueDeduplicates(t *testing.T) {
	q, _ := newQueue(t)

	added, err := q.Enqueue([]Item{item("a", 1), item("b", 1), item("a", 1)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}

	stats, err := q.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats[StatusPending] != 2 {
		t.Errorf("pending = %d, want 2", stats[StatusPending])
	}
}

// Re-discovery is the normal case in a recursive crawl and must never resurrect finished work.
func TestEnqueueDoesNotResetCompletedItems(t *testing.T) {
	q, _ := newQueue(t)

	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Complete(Of("a"), Resolution{Ref: HeadRef, SHA: "sha-a"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := q.Enqueue([]Item{item("a", 2)}); err != nil {
		t.Fatalf("re-Enqueue: %v", err)
	}

	stats, _ := q.Stats()
	if stats[StatusDone] != 1 || stats[StatusPending] != 0 {
		t.Errorf("stats = %v, want one done and nothing pending", stats)
	}
}

// Depth is path-dependent, so the shortest observed path is what bounds exploration.
func TestEnqueueKeepsShallowestDepth(t *testing.T) {
	q, s := newQueue(t)

	if _, err := q.Enqueue([]Item{item("a", 4)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := q.Enqueue([]Item{item("a", 2)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var depth int
	s.DB().QueryRow(`SELECT depth FROM abom_frontier WHERE node_key='a'`).Scan(&depth)
	if depth != 2 {
		t.Errorf("depth = %d, want 2", depth)
	}
}

func TestEnqueueRejectsBeyondMaxDepth(t *testing.T) {
	q, _ := newQueue(t)

	added, err := q.Enqueue([]Item{item("deep", 6), item("ok", 5)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if added != 1 {
		t.Errorf("added = %d, want 1 (depth 6 exceeds MaxDepth 5)", added)
	}
}

// Value is commonality, not proximity: a widely-depended-on dependency outranks a rarely used
// one. Narrowed on 2026-09-07 when depth became the outer key of the objective — completeness at
// a depth before anything deeper — so commonality now decides *within* a depth rather than
// across depths. The thing this protects is unchanged: score, not discovery order, ranks the
// work the crawl can choose between.
func TestClaimOrdersByCommonalityWithinADepth(t *testing.T) {
	q, s := newQueue(t)

	if _, err := q.Enqueue([]Item{item("rare-but-early", 4), item("common-but-late", 4)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	seedCommonality(t, s, "rare-but-early", 1, 1)
	seedCommonality(t, s, "common-but-late", 150, 90)

	if err := q.Rescore(); err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	claimed, err := q.Claim(2)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d items, want 2", len(claimed))
	}
	if claimed[0].NodeKey != "common-but-late" {
		t.Errorf("first claimed = %q, want common-but-late", claimed[0].NodeKey)
	}
}

// Commonality is only partly known when a node is first seen, so scores must improve as the
// graph fills in. This is the adaptive part of greedy exploration.
func TestRescoreReflectsNewlyLearnedCommonality(t *testing.T) {
	q, s := newQueue(t)

	if _, err := q.Enqueue([]Item{item("a", 1), item("b", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	seedCommonality(t, s, "a", 5, 3)
	seedCommonality(t, s, "b", 1, 1)
	if err := q.Rescore(); err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if first, _ := q.Claim(1); first[0].NodeKey != "a" {
		t.Fatalf("expected a first, got %q", first[0].NodeKey)
	}
	if err := q.Release(Of("a")); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// b turns out to be far more common than first believed.
	seedCommonality(t, s, "b", 200, 120)
	if err := q.Rescore(); err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if first, _ := q.Claim(1); first[0].NodeKey != "b" {
		t.Errorf("expected b to overtake a after rescoring, got %q", first[0].NodeKey)
	}
}

// A cheaper dependency of equal commonality is better value per unit of budget.
func TestScoreFavorsLowerCost(t *testing.T) {
	q, s := newQueue(t)

	cheap, expensive := item("cheap", 1), item("expensive", 1)
	cheap.CostEstimate, expensive.CostEstimate = 1, 20
	if _, err := q.Enqueue([]Item{cheap, expensive}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	seedCommonality(t, s, "cheap", 10, 5)
	seedCommonality(t, s, "expensive", 10, 5)
	if err := q.Rescore(); err != nil {
		t.Fatalf("Rescore: %v", err)
	}

	claimed, _ := q.Claim(1)
	if claimed[0].NodeKey != "cheap" {
		t.Errorf("first claimed = %q, want cheap", claimed[0].NodeKey)
	}
}

func TestClaimMarksItemsRunning(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if _, err := q.Claim(1); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// A second claim must not hand out the same work again.
	again, err := q.Claim(1)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("claimed %d items on the second call, want 0", len(again))
	}
}

func TestFailSchedulesRetryWithBackoff(t *testing.T) {
	q, s := newQueue(t)
	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := q.Claim(1); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := q.Fail(Of("a"), "network exploded"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	// Backoff must keep it out of the immediate queue.
	claimed, _ := q.Claim(1)
	if len(claimed) != 0 {
		t.Errorf("claimed a failed item before its retry time")
	}

	var attempts int
	var status string
	s.DB().QueryRow(`SELECT attempts, status FROM abom_frontier WHERE node_key='a'`).
		Scan(&attempts, &status)
	if attempts != 1 || status != StatusPending {
		t.Errorf("attempts=%d status=%q, want 1 and pending", attempts, status)
	}
}

// A repository with no workflows and no action manifest is a permanent answer, not a failure
// to retry every run.
func TestUnavailableIsNotRetried(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := q.Claim(1); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := q.Unavailable(Of("a"), "no .github directory"); err != nil {
		t.Fatalf("Unavailable: %v", err)
	}

	claimed, _ := q.Claim(10)
	if len(claimed) != 0 {
		t.Errorf("claimed an unavailable item")
	}
	stats, _ := q.Stats()
	if stats[StatusUnavailable] != 1 {
		t.Errorf("stats = %v, want one unavailable", stats)
	}
}

// Mutually-referencing dependencies are legal; enqueueing must not loop or duplicate.
func TestCyclesEnqueueOnce(t *testing.T) {
	q, _ := newQueue(t)

	a := Item{NodeKey: "a", Owner: "octo-org", Repo: "a", Depth: 1, DiscoveredFrom: "b"}
	b := Item{NodeKey: "b", Owner: "octo-org", Repo: "b", Depth: 1, DiscoveredFrom: "a"}
	if _, err := q.Enqueue([]Item{a, b}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := q.Enqueue([]Item{a, b}); err != nil {
		t.Fatalf("re-Enqueue: %v", err)
	}

	stats, _ := q.Stats()
	if stats[StatusPending] != 2 {
		t.Errorf("pending = %d, want 2", stats[StatusPending])
	}
}

func TestClaimRespectsLimit(t *testing.T) {
	q, _ := newQueue(t)
	var items []Item
	for _, k := range []string{"a", "b", "c", "d"} {
		items = append(items, item(k, 1))
	}
	if _, err := q.Enqueue(items); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	claimed, err := q.Claim(2)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Errorf("claimed %d, want 2", len(claimed))
	}
}

func TestEmptyQueueClaimsNothing(t *testing.T) {
	q, _ := newQueue(t)
	claimed, err := q.Claim(5)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed %d from an empty queue", len(claimed))
	}
}

// A mutable ref moves, so what the graph holds for it goes out of date without any change to
// the depending repositories. Detecting that is the whole point of recording the SHA.
func TestMutableRefBecomesDueForRecheck(t *testing.T) {
	q, _ := newQueue(t)

	if _, err := q.Enqueue([]Item{item("actions/checkout", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Complete(Of("actions/checkout"), Resolution{Ref: HeadRef, SHA: "old-sha"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if due, err := q.DueForRecheck(24*time.Hour, 10); err != nil {
		t.Fatalf("DueForRecheck: %v", err)
	} else if len(due) != 0 {
		t.Fatalf("freshly analyzed item is already due: %v", due)
	}

	q.Now = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }
	due, err := q.DueForRecheck(24*time.Hour, 10)
	if err != nil {
		t.Fatalf("DueForRecheck: %v", err)
	}
	if len(due) != 1 || due[0].NodeKey != "actions/checkout" || due[0].SHA != "old-sha" {
		t.Fatalf("due = %+v, want actions/checkout at old-sha", due)
	}
}

// A ref identical to its own commit cannot move, so probing it is wasted budget.
func TestImmutableRefIsNeverRechecked(t *testing.T) {
	q, _ := newQueue(t)

	if _, err := q.Enqueue([]Item{item("actions/checkout", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Complete(Of("actions/checkout"), Resolution{Ref: "abc123", SHA: "abc123"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	q.Now = func() time.Time { return time.Now().UTC().Add(365 * 24 * time.Hour) }
	due, err := q.DueForRecheck(24*time.Hour, 10)
	if err != nil {
		t.Fatalf("DueForRecheck: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("immutable ref queued for recheck: %+v", due)
	}
}

// A commit ref cannot move even when the checkout names another commit. That happens when the
// id is an annotated tag's own object, which a public lock was measured recording as its commit
// (`haskell-actions/setup@v2.12.1`, 2026-09-27): the checkout peels it, the two disagree, and a
// probe of the id finds no ref by that name, which reads as the dependency having vanished.
func TestACommitRefIsNeverRecheckedEvenWhenItsCheckoutNamedAnotherCommit(t *testing.T) {
	q, _ := newQueue(t)

	const (
		tagObject = "0f7370ccbc65f22514ec3e094f6601b8d61fbbf7"
		peeled    = "0f8e8c99d88aeb3fbfd523f1ef2c6f762d10d64d"
	)
	it := item("haskell-actions/setup", 1)
	it.Ref = tagObject
	if _, err := q.Enqueue([]Item{it}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Complete(At("haskell-actions/setup", tagObject),
		Resolution{Ref: tagObject, SHA: peeled}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	q.Now = func() time.Time { return time.Now().UTC().Add(365 * 24 * time.Hour) }
	due, err := q.DueForRecheck(24*time.Hour, 10)
	if err != nil {
		t.Fatalf("DueForRecheck: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("a commit ref queued for recheck: %+v", due)
	}
}

// Rechecking is a cheap probe; only actual drift should buy a full re-analysis.
func TestTouchDefersWithoutReanalyzing(t *testing.T) {
	q, _ := newQueue(t)

	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Complete(Of("a"), Resolution{Ref: HeadRef, SHA: "sha1"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	q.Now = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }
	if err := q.Touch(Of("a")); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	stats, _ := q.Stats()
	if stats[StatusDone] != 1 || stats[StatusPending] != 0 {
		t.Errorf("stats = %v, want the item still done", stats)
	}
	if due, _ := q.DueForRecheck(24*time.Hour, 10); len(due) != 0 {
		t.Errorf("touched item still due: %+v", due)
	}
}

// A moved ref returns to the queue and competes on commonality like anything else, so a stale
// dependency thousands of workflows use is re-analyzed before an obscure unanalyzed one.
func TestStaleRefRequeuesAndOutranksLowValueWork(t *testing.T) {
	q, s := newQueue(t)

	if _, err := q.Enqueue([]Item{item("actions/checkout", 1), item("obscure/action", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Complete(Of("actions/checkout"), Resolution{Ref: HeadRef, SHA: "old-sha"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	seedCommonality(t, s, "actions/checkout", 3000, 200)
	seedCommonality(t, s, "obscure/action", 1, 1)

	if err := q.MarkStale(Of("actions/checkout"), "new-sha"); err != nil {
		t.Fatalf("MarkStale: %v", err)
	}
	if err := q.Rescore(); err != nil {
		t.Fatalf("Rescore: %v", err)
	}

	claimed, err := q.Claim(2)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 2 || claimed[0].NodeKey != "actions/checkout" {
		t.Fatalf("claim order = %+v, want the stale high-commonality item first", claimed)
	}
}

// Until re-analysis actually replaces the data, the stored SHA still names the commit the graph
// describes. Overwriting it early would claim coverage of code that was never analyzed.
func TestMarkStaleKeepsTheAnalyzedSHA(t *testing.T) {
	q, s := newQueue(t)

	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Complete(Of("a"), Resolution{Ref: HeadRef, SHA: "old-sha"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := q.MarkStale(Of("a"), "new-sha"); err != nil {
		t.Fatalf("MarkStale: %v", err)
	}

	var sha, reason string
	if err := s.DB().QueryRow(`SELECT resolved_sha, reason FROM abom_frontier WHERE node_key='a'`).
		Scan(&sha, &reason); err != nil {
		t.Fatalf("reading row: %v", err)
	}
	if sha != "old-sha" {
		t.Errorf("resolved_sha = %q, want the SHA the graph was built from", sha)
	}
	if !strings.Contains(reason, "new-sha") {
		t.Errorf("reason = %q, want it to record the new SHA", reason)
	}
}

// Re-analysis should be visible as such, so a repeatedly drifting dependency can be told apart
// from one analyzed once.
func TestReanalysisCountsUp(t *testing.T) {
	q, s := newQueue(t)

	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	for _, sha := range []string{"sha1", "sha2", "sha3"} {
		if err := q.Complete(Of("a"), Resolution{Ref: HeadRef, SHA: sha}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}

	var count int
	var sha string
	if err := s.DB().QueryRow(
		`SELECT analysis_count, resolved_sha FROM abom_frontier WHERE node_key='a'`).
		Scan(&count, &sha); err != nil {
		t.Fatalf("reading row: %v", err)
	}
	if count != 3 {
		t.Errorf("analysis_count = %d, want 3", count)
	}
	if sha != "sha3" {
		t.Errorf("resolved_sha = %q, want the most recent analysis", sha)
	}
}

// seedCommonality records how many workflows and repositories depend on a node, which is what
// the queue scores by.
// Analyzing a reusable workflow always yields the steps its callers inherit, whereas most
// actions turn out to be JavaScript and yield nothing to an actions database. Between two
// dependencies of similar commonality, the one that can actually extend the graph is worth more.
func TestRescoreFavorsDependenciesThatCanYieldEdges(t *testing.T) {
	q, s := newQueue(t)
	if _, err := q.Enqueue([]Item{item("octo/shared", 1), item("octo/plain", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	seedCommonalityOfKind(t, s, "octo/shared", "reusable_workflow", 5, 5)
	seedCommonalityOfKind(t, s, "octo/plain", "action", 8, 8)

	if err := q.Rescore(); err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	claimed, err := q.Claim(2)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if len(claimed) != 2 || claimed[0].NodeKey != "octo/shared" {
		t.Errorf("claim order = %+v, want the reusable workflow first", claimed)
	}
}

// A composite action is a known container too: its own steps are part of what its callers pull
// in, so it is worth the same preference.
func TestRescoreFavorsCompositeActions(t *testing.T) {
	q, s := newQueue(t)
	if _, err := q.Enqueue([]Item{item("octo/composite", 1), item("octo/plain", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	seedCommonalityOfKind(t, s, "octo/composite", "composite_action", 5, 5)
	seedCommonalityOfKind(t, s, "octo/plain", "action", 8, 8)

	if err := q.Rescore(); err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	claimed, _ := q.Claim(2)

	if len(claimed) != 2 || claimed[0].NodeKey != "octo/composite" {
		t.Errorf("claim order = %+v, want the composite action first", claimed)
	}
}

// The yield preference must not displace commonality: something the whole organization depends
// on still comes first, however promising a rarely used dependency looks.
func TestCommonalityStillOutranksYieldPreference(t *testing.T) {
	q, s := newQueue(t)
	if _, err := q.Enqueue([]Item{item("octo/niche-shared", 1), item("actions/checkout", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	seedCommonalityOfKind(t, s, "octo/niche-shared", "reusable_workflow", 1, 1)
	seedCommonalityOfKind(t, s, "actions/checkout", "action", 200, 200)

	if err := q.Rescore(); err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	claimed, _ := q.Claim(2)

	if len(claimed) != 2 || claimed[0].NodeKey != "actions/checkout" {
		t.Errorf("claim order = %+v, want the widely depended-on action first", claimed)
	}
}

// The weight is configurable so the preference can be tuned or switched off entirely.
func TestContainerWeightIsConfigurable(t *testing.T) {
	q, s := newQueue(t)
	q.ContainerWeight = 1 // no preference
	if _, err := q.Enqueue([]Item{item("octo/shared", 1), item("octo/plain", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	seedCommonalityOfKind(t, s, "octo/shared", "reusable_workflow", 5, 5)
	seedCommonalityOfKind(t, s, "octo/plain", "action", 8, 8)

	if err := q.Rescore(); err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	claimed, _ := q.Claim(2)

	if len(claimed) != 2 || claimed[0].NodeKey != "octo/plain" {
		t.Errorf("claim order = %+v, want commonality alone to decide", claimed)
	}
}

// An interrupted run leaves items marked running, and nothing else ever moves them again:
// Claim only ever looks at pending. Without a lease one Ctrl-C strands that work permanently.
func TestAStaleClaimReturnsToTheQueue(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := q.Claim(1); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	q.Now = func() time.Time { return time.Now().UTC().Add(2 * time.Hour) }
	n, err := q.ReclaimStale(time.Hour)
	if err != nil {
		t.Fatalf("ReclaimStale: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed %d items, want 1", n)
	}

	claimed, _ := q.Claim(1)
	if len(claimed) != 1 || claimed[0].NodeKey != "a" {
		t.Errorf("claimed %+v, want the reclaimed item to be available again", claimed)
	}
}

// Nothing was learned from an interrupted attempt, so reclaiming must not spend an attempt or
// push the item into backoff. Otherwise a few restarts would exhaust the retry budget of work
// that was never actually tried.
func TestReclaimingAStaleClaimDoesNotCountAsAnAttempt(t *testing.T) {
	q, s := newQueue(t)
	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := q.Claim(1); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	q.Now = func() time.Time { return time.Now().UTC().Add(2 * time.Hour) }
	if _, err := q.ReclaimStale(time.Hour); err != nil {
		t.Fatalf("ReclaimStale: %v", err)
	}

	var attempts int
	var next any
	if err := s.DB().QueryRow(
		`SELECT attempts, next_attempt_at FROM abom_frontier WHERE node_key='a'`).
		Scan(&attempts, &next); err != nil {
		t.Fatalf("reading item: %v", err)
	}
	if attempts != 0 || next != nil {
		t.Errorf("attempts=%d next_attempt_at=%v, want 0 and unset", attempts, next)
	}
}

// A claim that outlived the process holding it cannot be in flight, because the store admits a
// single writer. Waiting out the lease leaves a stopped queue reporting work nobody is doing: a
// restart mid-cycle stranded 20 dependencies that the interface showed as being analyzed, and
// the same screen said discovery had never started.
func TestReclaimOrphanedTakesBackEvenAClaimMadeMomentsAgo(t *testing.T) {
	q, s := newQueue(t)
	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := q.Claim(1); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	n, err := q.ReclaimOrphaned()
	if err != nil {
		t.Fatalf("ReclaimOrphaned: %v", err)
	}
	if n != 1 {
		t.Errorf("reclaimed %d, want the orphaned claim back", n)
	}

	var status string
	var attempts int
	if err := s.DB().QueryRow(
		`SELECT status, attempts FROM abom_frontier WHERE node_key='a'`).
		Scan(&status, &attempts); err != nil {
		t.Fatalf("reading item: %v", err)
	}
	if status != StatusPending {
		t.Errorf("status = %q, want the claim handed back to the queue", status)
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, want an interrupted attempt not charged", attempts)
	}
}

// A claim still within its lease belongs to a worker that is using it; handing it to a second
// worker would clone the clone and the database build.
func TestAFreshClaimIsNotReclaimed(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := q.Claim(1); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	n, err := q.ReclaimStale(time.Hour)
	if err != nil {
		t.Fatalf("ReclaimStale: %v", err)
	}
	if n != 0 {
		t.Errorf("reclaimed %d items, want 0 while the lease holds", n)
	}
}

// Exiting because nothing is claimable right now reports the same thing as an empty queue,
// which is how a run abandons work in backoff while calling itself finished.
func TestNextAttemptAtReportsWorkWaitingInBackoff(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := q.Claim(1); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := q.Fail(Of("a"), "network exploded"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	at, ok, err := q.NextAttemptAt()
	if err != nil {
		t.Fatalf("NextAttemptAt: %v", err)
	}
	if !ok {
		t.Fatal("reported no pending work while an item waits in backoff")
	}
	if !at.After(time.Now().UTC()) {
		t.Errorf("next attempt at %v, want a time in the future", at)
	}
}

func TestNextAttemptAtReportsNothingWhenTheQueueIsDrained(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Complete(Of("a"), Resolution{Ref: HeadRef, SHA: "sha-a"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if _, ok, err := q.NextAttemptAt(); err != nil {
		t.Fatalf("NextAttemptAt: %v", err)
	} else if ok {
		t.Error("reported pending work with nothing left in the queue")
	}
}

// A `uses:` assembled from an expression extracts with the expression where the account should
// be. The reference is real and belongs in the graph verbatim, but no such repository exists, so
// queueing it spends a checkout to learn what its name already said.
func TestAReferenceThatNamesNoRepositoryIsNeverQueued(t *testing.T) {
	q, s := newQueue(t)

	if _, err := q.Enqueue([]Item{
		{NodeKey: "${{ github.repository }}/setup", Owner: "${{ github.repository }}",
			Repo: "setup", Depth: 1},
		{NodeKey: "actions/checkout", Owner: "actions", Repo: "checkout", Depth: 1},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	claimed, err := q.Claim(10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].NodeKey != "actions/checkout" {
		t.Errorf("claimed %+v, want only the reference that names a real repository", claimed)
	}

	var status, reason string
	if err := s.DB().QueryRow(
		`SELECT status, COALESCE(reason,'') FROM abom_frontier WHERE node_key='${{ github.repository }}/setup'`).
		Scan(&status, &reason); err != nil {
		t.Fatalf("reading the item: %v", err)
	}
	if status != StatusUnavailable {
		t.Errorf("status = %q, want %q", status, StatusUnavailable)
	}
	if reason == "" {
		t.Error("recorded it as unanalyzable without saying why")
	}
}

// Settled the moment it arrives is still settled. Leaving the time empty makes it the only
// terminal row a view cannot date, so "when did we give up on this" has no answer for exactly
// the entries whose answer was immediate.
func TestAReferenceRejectedOnArrivalIsStillDated(t *testing.T) {
	q, s := newQueue(t)

	if _, err := q.Enqueue([]Item{
		{NodeKey: "${{ github.repository }}/setup", Owner: "${{ github.repository }}",
			Repo: "setup", Depth: 1},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var completed string
	if err := s.DB().QueryRow(
		`SELECT COALESCE(completed_at,'') FROM abom_frontier WHERE node_key='${{ github.repository }}/setup'`).
		Scan(&completed); err != nil {
		t.Fatalf("reading the item: %v", err)
	}
	if completed == "" {
		t.Error("settled the reference without recording when")
	}
}

func seedCommonality(t *testing.T, s *store.Store, nodeKey string, workflows, repos int) {
	t.Helper()
	seedCommonalityOfKind(t, s, nodeKey, "action", workflows, repos)
}

// seedCommonalityOfKind records commonality for a node of a given kind, which is what decides
// whether analyzing it can yield anything.
func seedCommonalityOfKind(
	t *testing.T, s *store.Store, nodeKey, kind string, workflows, repos int,
) {
	t.Helper()
	w := store.NewWriter(s, 0)
	if err := w.AddNode(store.Node{Key: nodeKey, Kind: kind, IsExternal: true}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var id int64
	if err := s.DB().QueryRow(`SELECT row_id FROM abom_node WHERE node_key=?`, nodeKey).
		Scan(&id); err != nil {
		t.Fatalf("resolving node: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_blast_radius
			(node_row_id, dependent_repos, dependent_workflows, direct_dependents, max_depth)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(node_row_id) DO UPDATE SET
			dependent_repos = excluded.dependent_repos,
			dependent_workflows = excluded.dependent_workflows`,
		id, repos, workflows, workflows); err != nil {
		t.Fatalf("seeding blast radius: %v", err)
	}
}
