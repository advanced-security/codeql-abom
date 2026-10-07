package expand

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/frontier"
)

// gate releases every goroutine only once n of them have arrived.
//
// Two references of one repository race in a real crawl: both resolve to one commit before
// either has been recorded, and whichever prepares second clones what the first is already
// fetching. The race is real but not reproducible, and a test that only sometimes exercises the
// case it names is not evidence.
type gate struct {
	t     *testing.T
	want  int
	mu    sync.Mutex
	left  int
	open  chan struct{}
	timed bool
}

func newGate(t *testing.T, n int) *gate {
	return &gate{t: t, want: n, left: n, open: make(chan struct{})}
}

func (g *gate) arrive() {
	g.mu.Lock()
	g.left--
	if g.left == 0 {
		close(g.open)
	}
	g.mu.Unlock()
	// Bounded, so a test that claims fewer jobs than it gated fails rather than hanging. The
	// timeout is a failure and not a release: releasing quietly is how a one-fetch assertion
	// passes without two resolvers ever having been inside the window it is about.
	select {
	case <-g.open:
	case <-time.After(10 * time.Second):
		g.timedOut()
	}
}

// timedOut reports the shortfall once, from whichever goroutine noticed it. Errorf rather than
// Fatalf: this runs off the test's own goroutine, where Fatalf would only end that one.
func (g *gate) timedOut() {
	g.t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.timed {
		return
	}
	g.timed = true
	g.t.Errorf("only %d of %d participants reached the gate, so the concurrency this test "+
		"names never happened and its assertions prove nothing", g.want-g.left, g.want)
}

// runWith is h.run with the round spelled out, for tests that need concurrency or a second
// query identity.
func (h *harness) runWith(opts Options) Stats {
	h.t.Helper()
	if opts.QueryPath == "" {
		opts.QueryPath = "AbomEdges.ql"
	}
	if opts.QueryID == "" {
		opts.QueryID = "abom/edges"
	}
	if opts.WorkDir == "" {
		opts.WorkDir = h.t.TempDir()
	}
	stats, err := h.expander.Run(context.Background(), opts)
	if err != nil {
		h.t.Fatalf("Run: %v", err)
	}
	return stats
}

// retainCheckouts gives the expander a cache that outlives a round, which is what a configured
// crawl has.
func (h *harness) retainCheckouts() *CheckoutCache {
	h.t.Helper()
	c := &CheckoutCache{Dir: filepath.Join(h.t.TempDir(), "checkouts")}
	h.expander.Cache = c
	return c
}

func (h *harness) fetchCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.fetched)
}

// itemAt is item with the reference that named it. The frontier is keyed on the pair, so two
// references of one repository are two rows and two batches.
func itemAt(owner, repo, subpath, ref string, depth int) frontier.Item {
	it := item(owner, repo, subpath, depth)
	it.Ref = ref
	return it
}

// The fetch identity is a commit, not the reference that named it. `run_key` hashes `db_commit`
// with no reference in it, so two references at one commit are one evaluation — and until this
// landed they were still two clones.
func TestTwoReferencesAtOneCommitAreFetchedOnce(t *testing.T) {
	h := newHarness(t)
	h.retainCheckouts()
	h.probe.hold(newGate(t, 2))
	h.enqueue(
		itemAt("gradle", "actions", "setup-gradle", "v4", 1),
		itemAt("gradle", "actions", "setup-gradle", "v3", 1),
	)

	h.runWith(Options{Limit: 10, Concurrency: 2})

	if got := h.fetchCount(); got != 1 {
		t.Errorf("fetched %d times (%v), want one clone for one commit", got, h.fetched)
	}
}

// A second crawl over an unchanged corpus must not clone. The analysis decision is separate and
// stays where it was: a round holding a query the ledger has never seen still builds and runs,
// it just does not re-fetch a commit it already holds.
func TestASecondRoundReusesTheCommitItAlreadyFetched(t *testing.T) {
	h := newHarness(t)
	h.retainCheckouts()
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	fetched := h.fetchCount()
	built := h.databasesBuilt()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued, so no second round would run")
	}
	h.runWith(Options{Limit: 100, QueryID: "abom/edges-v2"})

	if again := h.fetchCount() - fetched; again != 0 {
		t.Errorf("a second round cloned %d repositories, want none", again)
	}
	if h.databasesBuilt() == built {
		t.Error("a cache hit suppressed the analysis; reuse of a checkout is not a skip")
	}
}

// A tag moves. Keying on the commit it resolved to is what makes the cache safe; keying on the
// tag would serve the old tree forever with nothing to say it had.
func TestAMovedTagIsFetchedAtItsNewCommit(t *testing.T) {
	h := newHarness(t)
	cache := h.retainCheckouts()
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	fetched := h.fetchCount()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued")
	}
	h.probe.moveEverythingTo("moved-sha")
	h.runWith(Options{Limit: 100})

	if again := h.fetchCount() - fetched; again == 0 {
		t.Error("a moved tag was served from the cache, so the old commit would be analyzed")
	}
	if _, ok := cache.Reuse("gradle", "actions", "moved-sha", h.SparsePatterns()); !ok {
		t.Error("the new commit was not retained, so the next round would fetch it again")
	}
	if _, sha, _ := statusOf(t, h.store, "gradle/actions/setup-gradle"); sha != "moved-sha" {
		t.Errorf("recorded sha = %q, want the commit the tag moved to", sha)
	}
}

// A tree fetched under narrower patterns is a tree that is missing files, which reads downstream
// as a repository that does not contain them.
//
// The query identity deliberately does not move here. A widening is a code change and nothing
// else: the query, the packs and the CodeQL are the ones that already ran, and the commit is on
// record with a digest measured under the old filter. Every reuse decision therefore says
// "already done" unless the filter is part of what they are asked about, and the round settles
// without ever fetching the files the widening was for.
func TestAWidenedSparseFilterIsNotServedFromTheCache(t *testing.T) {
	h := newHarness(t)
	h.retainCheckouts()
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	fetched := h.fetchCount()
	built := h.databasesBuilt()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued")
	}
	h.widenPatterns("/.github/", "action.yml", "action.yaml")
	h.runWith(Options{Limit: 100})

	if again := h.fetchCount() - fetched; again == 0 {
		t.Error("a wider filter was answered from a narrower tree, which is missing files")
	}
	if h.databasesBuilt() == built {
		t.Error("the wider tree was fetched and not analyzed, so whatever the added paths hold " +
			"is still absent from the graph")
	}
}

// The tree outlives the unit that fetched it. Deleting it after each unit is what made every
// cycle re-clone what the last one threw away.
func TestARetainedCheckoutOutlivesTheUnitThatFetchedIt(t *testing.T) {
	h := newHarness(t)
	cache := h.retainCheckouts()
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))

	h.runWith(Options{Limit: 10})

	dir, ok := cache.Reuse("gradle", "actions", "sha-actions", h.SparsePatterns())
	if !ok {
		t.Fatal("nothing was retained for the commit that was just analyzed")
	}
	if _, err := os.Stat(filepath.Join(dir, ".github", "workflows", "ci.yml")); err != nil {
		t.Errorf("the retained tree is missing what was fetched into it: %v", err)
	}
}

// A path has to read back to what it holds. An underscore join cannot: `foo_bar_baz` is both
// `foo/bar_baz` and `foo_bar/baz`, and repository names contain underscores.
func TestACachePathReadsBackToOwnerRepositoryAndCommit(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir()}
	patterns := []string{"/.github/"}

	first := retain(t, cache, "foo", "bar_baz", "aaaa1111", patterns, 16)
	second := retain(t, cache, "foo_bar", "baz", "aaaa1111", patterns, 16)

	commit := filepath.Join(cache.Dir, "foo", "bar_baz", "aaaa1111-"+filterID(patterns))
	if first != commit {
		t.Errorf("entry = %q, want %q", first, commit)
	}
	if first == second {
		t.Error("foo/bar_baz and foo_bar/baz share an entry, so one would serve the other's tree")
	}
}

// The same commit under two filters is two sets of files. One slot for both had each fetch
// delete the other, so a crawl that alternated filters cloned every time and could take the
// source out from under a database build that was reading it.
func TestTwoFiltersOverOneCommitAreHeldSeparately(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir()}
	narrow := []string{"/.github/"}
	wide := []string{"/.github/", "action.yml"}

	first := retain(t, cache, "actions", "checkout", "aaaa1111", narrow, 16)
	second := retain(t, cache, "actions", "checkout", "aaaa1111", wide, 16)

	if first == second {
		t.Fatal("both filters share an entry, so the second fetch replaced the first's tree")
	}
	if _, ok := cache.Reuse("actions", "checkout", "aaaa1111", narrow); !ok {
		t.Error("the narrow tree is gone, so switching back would clone it again")
	}
	if _, ok := cache.Reuse("actions", "checkout", "aaaa1111", wide); !ok {
		t.Error("the wide tree is gone")
	}
}

// Owner and repository names come from somebody else's workflow file, so they are input. A name
// that walks out of the cache directory is refused rather than sanitized into something else.
func TestACacheKeyCannotEscapeTheCacheDirectory(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir()}
	patterns := []string{"/.github/"}

	staged, err := cache.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := cache.Retain(staged, "..", "..", "aaaa1111", patterns); err == nil {
		t.Error("a traversing owner was accepted, so a fetch could be written outside the cache")
	}
	if _, ok := cache.Reuse("actions", "check/../../out", "aaaa1111", patterns); ok {
		t.Error("a traversing repository name resolved to an entry")
	}
}

// A commit is hex, and the same commit written in two cases is one commit. Two entries for it
// would fetch it twice and hold it twice.
func TestACommitIsCachedWithoutRegardToItsCase(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir()}
	patterns := []string{"/.github/"}
	retain(t, cache, "actions", "checkout", "AAAA1111", patterns, 16)

	if _, ok := cache.Reuse("actions", "checkout", "aaaa1111", patterns); !ok {
		t.Error("a commit written in lower case missed an entry written in upper case")
	}
}

// A cache with no ceiling is a disk leak that presents as a crawl failure weeks later. The
// oldest use goes first, because the frontier keeps naming the commits it still works on.
func TestTheCeilingEvictsTheLeastRecentlyUsedFirst(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir(), MaxBytes: 3000}
	patterns := []string{"/.github/"}
	oldest := retain(t, cache, "actions", "checkout", "aaaa1111", patterns, 1024)
	middle := retain(t, cache, "actions", "cache", "bbbb2222", patterns, 1024)
	newest := retain(t, cache, "actions", "setup-node", "cccc3333", patterns, 1024)
	usedAt(t, oldest, time.Now().Add(-3*time.Hour))
	usedAt(t, middle, time.Now().Add(-2*time.Hour))
	usedAt(t, newest, time.Now().Add(-time.Hour))

	evicted, err := cache.Trim()
	if err != nil {
		t.Fatalf("Trim: %v", err)
	}

	if evicted != 1 {
		t.Errorf("evicted %d entries, want the one that took the cache over its ceiling", evicted)
	}
	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Error("the least recently used entry survived the ceiling")
	}
	for _, kept := range []string{middle, newest} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was evicted while the cache was already under its ceiling", kept)
		}
	}
}

// Eviction costs a re-clone, so a cache inside its ceiling is left alone.
func TestACacheUnderItsCeilingIsLeftAlone(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir(), MaxBytes: 1 << 20}
	patterns := []string{"/.github/"}
	entry := retain(t, cache, "actions", "checkout", "aaaa1111", patterns, 1024)

	evicted, err := cache.Trim()
	if err != nil {
		t.Fatalf("Trim: %v", err)
	}
	if evicted != 0 {
		t.Errorf("evicted %d entries from a cache under its ceiling", evicted)
	}
	if _, err := os.Stat(entry); err != nil {
		t.Errorf("entry removed: %v", err)
	}
}

// A crawl that was killed mid-fetch leaves a staged tree nothing will ever promote. Left there
// it is occupancy the ceiling does not account for, which is the leak this ceiling exists to
// stop.
func TestAnAbandonedStagedFetchIsReclaimed(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir(), MaxBytes: 1 << 20}
	staged, err := cache.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	writeTree(t, stagedTree(staged), 4096)
	abandoned := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(staged, abandoned, abandoned); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if _, err := cache.Trim(); err != nil {
		t.Fatalf("Trim: %v", err)
	}

	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("an abandoned staged fetch survived, so its bytes are unaccounted for")
	}
}

// A fetch still in flight is not abandoned. Reclaiming it would delete the tree out from under
// the database build that is reading it.
func TestAStagedFetchInFlightIsNotReclaimed(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir(), MaxBytes: 1 << 20}
	staged, err := cache.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	writeTree(t, stagedTree(staged), 4096)

	if _, err := cache.Trim(); err != nil {
		t.Fatalf("Trim: %v", err)
	}

	if _, err := os.Stat(staged); err != nil {
		t.Errorf("a staged fetch in flight was reclaimed: %v", err)
	}
}

// retain puts a tree of the given size in the cache and returns its entry.
func retain(
	t *testing.T, c *CheckoutCache, owner, repo, sha string, patterns []string, size int,
) string {
	t.Helper()
	staged, err := c.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	writeTree(t, stagedTree(staged), size)
	tree, err := c.Retain(staged, owner, repo, sha, patterns)
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	return filepath.Dir(tree)
}

func writeTree(t *testing.T, dir string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	body := strings.Repeat("y", size)
	if err := os.WriteFile(
		filepath.Join(dir, ".github", "workflows", "ci.yml"), []byte(body), 0o644); err != nil {
		t.Fatalf("writing the tree: %v", err)
	}
}

// usedAt dates an entry, which is what the ceiling evicts by.
func usedAt(t *testing.T, entry string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(filepath.Join(entry, cacheMarker), when, when); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

// A commit the probe could not name is promoted under that commit's lock too. Without it the two
// units that cloned one commit promote at once, and the entry one of them is analyzing can be
// removed by the other's promotion.
func TestAnUnresolvedReferencePromotesUnderTheCommitLock(t *testing.T) {
	h := newHarness(t)
	cache := h.retainCheckouts()
	h.probe.failEverything()
	h.enqueue(
		itemAt("gradle", "actions", "setup-gradle", "v4", 1),
		itemAt("gradle", "actions", "setup-gradle", "v3", 1),
	)

	h.runWith(Options{Limit: 10, Concurrency: 2})

	if got := h.fetchCount(); got != 2 {
		t.Fatalf("fetched %d times, want one clone per reference nothing could resolve", got)
	}
	if got := cache.Hits(); got != 1 {
		t.Errorf("cache hits = %d, want the second promotion to find the first's tree", got)
	}
}

// A commit is hex, and hex has two cases. Recognizing only one leaves a reference that cannot
// move looking unresolvable, so it is cloned again every cycle.
func TestACommitPinnedInUpperCaseIsNotFetchedTwice(t *testing.T) {
	h := newHarness(t)
	h.retainCheckouts()
	pinned := strings.ToUpper("0ad4b8fadaa221de15dcec353f45205ec38ea70b")
	h.enqueue(itemAt("actions", "checkout", "", pinned, 1))
	h.drain()

	fetched := h.fetchCount()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued, so no second round would run")
	}
	h.runWith(Options{Limit: 100, QueryID: "abom/edges-v2"})

	if again := h.fetchCount() - fetched; again != 0 {
		t.Errorf("a commit pinned in upper case was cloned %d more times", again)
	}
}

// A promotion that cannot happen still owns the staging directory it fetched into. Left there,
// the whole checkout — `.git` included — occupies the cache until the abandonment sweep.
func TestAFailedPromotionLeavesNothingInStaging(t *testing.T) {
	h := newHarness(t)
	cache := h.retainCheckouts()
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	// A file where the entry's parent has to be is a promotion the cache cannot perform, which
	// is the failure the round has to survive without leaving its fetch behind.
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("preparing the cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cache.Dir, "gradle"), []byte("x"), 0o600); err != nil {
		t.Fatalf("blocking the entry: %v", err)
	}

	h.runWith(Options{Limit: 10})

	if h.databasesBuilt() == 0 {
		t.Error("a failed promotion stopped the analysis, which the fetched tree can still serve")
	}
	staged, err := os.ReadDir(filepath.Join(cache.Dir, stagingDir))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading staging: %v", err)
	}
	if len(staged) != 0 {
		t.Errorf("%d staged fetches survived a failed promotion", len(staged))
	}
}

// The per-commit locks are bookkeeping, not state: a crawl names millions of commits, and a
// process that keeps a mutex for each one grows without bound for the life of the server.
func TestACommitLockIsForgottenOnceNobodyHoldsIt(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir()}

	for _, sha := range []string{"aaaa1111", "bbbb2222", "cccc3333"} {
		release := cache.Hold("actions", "checkout", sha)
		release()
	}

	cache.mu.Lock()
	held := len(cache.holds)
	cache.mu.Unlock()
	if held != 0 {
		t.Errorf("%d commit locks are still held after every holder released", held)
	}
}

// A held tree is evidence only while it is still the commit's content. On the live cache, 7 of 912
// had been rewritten in place by another tool, and the crawl reused two of them, so analyses were
// recorded of content those commits never held.
func TestAHeldTreeEditedSinceItWasFetchedIsNotReused(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir()}
	patterns := []string{"/.github/"}
	entry := retain(t, cache, "octo", "app", "aaaa1111", patterns, 16)
	if _, ok := cache.Reuse("octo", "app", "aaaa1111", patterns); !ok {
		t.Fatal("an untouched tree was not reused, so this test would pass against a cache that reuses nothing")
	}

	ci := filepath.Join(stagedTree(entry), ".github", "workflows", "ci.yml")
	if err := os.WriteFile(ci, []byte("uses: $/x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if tree, ok := cache.Reuse("octo", "app", "aaaa1111", patterns); ok {
		t.Errorf("reused %s after its content changed", tree)
	}
	// The fetch that replaces it is kept, rather than discarded in favor of the edited tree.
	fresh := retain(t, cache, "octo", "app", "aaaa1111", patterns, 16)
	body, err := os.ReadFile(filepath.Join(stagedTree(fresh), ".github", "workflows", "ci.yml"))
	if err != nil || strings.Contains(string(body), "$/") {
		t.Errorf("the edited tree survived a fresh fetch: %q, %v", body, err)
	}
}

// An entry retained before trees were verified cannot be checked, so it is fetched again once.
func TestAHeldTreeWithNothingToVerifyItIsFetchedAgain(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir()}
	patterns := []string{"/.github/"}
	entry := retain(t, cache, "octo", "app", "aaaa1111", patterns, 16)
	legacy := `{"owner":"octo","repo":"app","commit":"aaaa1111","patterns":["/.github/"],"bytes":16}`
	if err := os.WriteFile(filepath.Join(entry, cacheMarker), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := cache.Reuse("octo", "app", "aaaa1111", patterns); ok {
		t.Error("reused a tree whose content was never recorded")
	}
}

// A commit with nothing to index is still a verified tree, and must not be fetched every round.
func TestAnEmptyHeldTreeIsStillReused(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir()}
	patterns := []string{"/.github/"}
	staged, err := cache.Stage()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stagedTree(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Retain(staged, "octo", "empty", "bbbb2222", patterns); err != nil {
		t.Fatal(err)
	}

	if _, ok := cache.Reuse("octo", "empty", "bbbb2222", patterns); !ok {
		t.Error("an empty tree was not reused")
	}
}
