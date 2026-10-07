package expand

import (
	"context"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/lockfile"
	"github.com/advanced-security/codeql-abom/internal/store"
	native "github.com/github/actions-lockfile/go/pkg/lockfile"
)

// harnessTrees lists what the harness's Fetch would check out at a commit, without fetching it.
type harnessTrees struct {
	h         *harness
	mu        sync.Mutex
	reads     []string
	truncated bool
	failed    bool
}

func (h *harness) readTrees() *harnessTrees {
	trees := &harnessTrees{h: h}
	h.expander.Trees = trees
	return trees
}

func (tr *harnessTrees) ReadTree(_ context.Context, owner, repo, commit string) (Listing, error) {
	tr.mu.Lock()
	tr.reads = append(tr.reads, owner+"/"+repo+"@"+commit)
	truncated, failed := tr.truncated, tr.failed
	tr.mu.Unlock()
	if failed {
		return Listing{}, errTransient
	}
	_, content := tr.h.probe.state()
	tr.h.mu.Lock()
	files := map[string]string{".github/workflows/ci.yml": content}
	maps.Copy(files, tr.h.files[owner+"/"+repo])
	tr.h.mu.Unlock()
	l := listingOf(files)
	l.Truncated = truncated
	return l, nil
}

func (tr *harnessTrees) count() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return len(tr.reads)
}

func (h *harness) fetchedSince(n int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.fetched[n:])
}

// analyzedThenMoved analyzes a repository, puts it back in the queue and moves every reference to
// a commit nothing has fetched, which is a release landing on a tag.
func analyzedThenMoved(t *testing.T, prepare func(*harness)) (*harness, *harnessTrees, int) {
	t.Helper()
	h := newHarness(t)
	trees := h.readTrees()
	if prepare != nil {
		prepare(h)
	}
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued, so no reference could move")
	}
	fetched := h.fetchCount()
	h.probe.moveEverythingTo("released-sha")
	return h, trees, fetched
}

// A tag moved over a rebuilt `dist/` touches nothing the extractor reads. One listing says so,
// and the clone it would otherwise take is the most expensive read the crawl makes.
func TestAMovedRefToIdenticalContentSettlesWithoutAClone(t *testing.T) {
	h, trees, fetched := analyzedThenMoved(t, nil)
	read := trees.count()

	stats := h.run(100)

	if again := h.fetchedSince(fetched); len(again) != 0 {
		t.Errorf("identical content was cloned: %v", again)
	}
	if stats.TreeReads == 0 || stats.TreeReads != trees.count()-read {
		t.Errorf("the round reported %d listings and read %d; the cost it paid is what it reports",
			stats.TreeReads, trees.count()-read)
	}
	status, sha, _ := statusOf(t, h.store, "gradle/actions/setup-gradle")
	if status != frontier.StatusDone || sha != "released-sha" {
		t.Errorf("setup-gradle is %s at %q, want done at the commit the reference moved to", status, sha)
	}
	got, ok, err := h.store.CommitContentDigest("gradle/actions", "released-sha")
	if err != nil {
		t.Fatalf("CommitContentDigest: %v", err)
	}
	want, _, _ := h.store.CommitContentDigest("gradle/actions", "sha-actions")
	if !ok || got != want {
		t.Errorf("the settled commit records content %q, want the %q of the commit it matched", got, want)
	}
}

// The mirror image: content that moved is content nobody has analyzed.
func TestAMovedRefToDifferentContentIsStillCloned(t *testing.T) {
	h, trees, fetched := analyzedThenMoved(t, nil)
	h.probe.rewriteContent("name: ci\non: pull_request\n")

	h.run(100)

	if !slices.Contains(h.fetchedSince(fetched), "gradle/actions") {
		t.Error("changed content was settled without a clone")
	}
	if trees.count() == 0 {
		t.Error("no listing was read, so the comparison never ran")
	}
}

// Unknown is not identical. A listing GitHub cut short, or one that never arrived, describes no
// tree, and settling on it would waive a commit nobody has read.
func TestAnUnknownListingSettlesNothing(t *testing.T) {
	for name, spoil := range map[string]func(*harnessTrees){
		"truncated": func(tr *harnessTrees) { tr.truncated = true },
		"failed":    func(tr *harnessTrees) { tr.failed = true },
	} {
		t.Run(name, func(t *testing.T) {
			h, trees, fetched := analyzedThenMoved(t, nil)
			spoil(trees)

			h.run(100)

			if !slices.Contains(h.fetchedSince(fetched), "gradle/actions") {
				t.Error("an unknown listing settled the commit without a clone")
			}
			if trees.count() == 0 {
				t.Error("no listing was read, so its being unknown was never what declined it")
			}
		})
	}
}

// A digest recorded before blob ids were hashed names content in a scheme no listing reproduces.
// Here it even shares the listing's hash, and the scheme label alone has to keep them apart.
func TestARecordOfTheOldSchemeNeverMatchesAListing(t *testing.T) {
	h, trees, fetched := analyzedThenMoved(t, nil)
	if _, err := h.store.DB().Exec(`UPDATE abom_commit_content
		SET indexed_digest = REPLACE(indexed_digest, ?, ':')`, ":"+treeScheme+":"); err != nil {
		t.Fatalf("writing the records in the old form: %v", err)
	}
	// One comparable record in the repository, so that a listing is worth reading at all, and the
	// moved commit's own lock reading, so that only the scheme is left to refuse the match.
	if err := h.store.RecordCommitContent("gradle/actions", "unrelated-sha",
		scopedDigest(h.expander.sourceFilter(), treeScheme+":"+strings.Repeat("0", 64))); err != nil {
		t.Fatalf("RecordCommitContent: %v", err)
	}
	if err := h.store.RecordLockReading("gradle/actions", "released-sha", lockfile.Reader,
		[]store.LockCoverage{{Coverage: string(lockfile.NotOnboarded)}}, store.LockGraph{}); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}

	h.run(100)

	if !slices.Contains(h.fetchedSince(fetched), "gradle/actions") {
		t.Error("a listing settled against a record of the old scheme")
	}
	if trees.count() == 0 {
		t.Error("no listing was read, so the scheme was never what refused the match")
	}
}

// A lock is read from a checkout, and a commit settled without one still has to say what its
// lock selects. Inside the digested tree an identical digest is an identical lock, so the reading
// carries over from the commit it matched — every row of it, graph included.
func TestAnIdenticalCommitCarriesTheLockReadingOfTheOneItMatches(t *testing.T) {
	h, _, fetched := analyzedThenMoved(t, func(h *harness) { h.lockFor("gradle/actions", rootLock) })

	h.run(100)

	if slices.Contains(h.fetchedSince(fetched), "gradle/actions") {
		t.Fatal("the commit was cloned, so nothing was carried")
	}
	analyzed, settled := h.lockRows("sha-actions"), h.lockRows("released-sha")
	if !slices.Contains(analyzed, "|covered") {
		t.Fatalf("the analyzed commit's lock reads %v, so the fixture carries nothing worth checking", analyzed)
	}
	if !slices.Equal(analyzed, settled) {
		t.Errorf("coverage at the settled commit is %v, want the matched commit's %v", settled, analyzed)
	}
	want, err := h.store.LockGraphAt("gradle/actions", "sha-actions")
	if err != nil {
		t.Fatalf("LockGraphAt: %v", err)
	}
	got, err := h.store.LockGraphAt("gradle/actions", "released-sha")
	if err != nil {
		t.Fatalf("LockGraphAt: %v", err)
	}
	if len(want.Dependencies) == 0 || !reflect.DeepEqual(got, want) {
		t.Errorf("the settled commit's lock graph is %+v, want %+v", got, want)
	}
}

// Where the filter leaves the lock's directory out, two identical digests say nothing about two
// locks, so the commit still needs its own reading.
func TestALockOutsideTheDigestedTreeIsStillReadFromACheckout(t *testing.T) {
	h, trees, fetched := analyzedThenMoved(t, func(h *harness) {
		h.widenPatterns("/.github/workflows/ci.yml")
	})

	h.run(100)

	if !slices.Contains(h.fetchedSince(fetched), "gradle/actions") {
		t.Error("the commit settled with a lock reading its digest does not cover")
	}
	if trees.count() == 0 {
		t.Error("no listing was read, so the lock was never what declined it")
	}
}

// A listing is spent only where it could match something. A repository's first commit has nothing
// comparable on record, and reading its tree would add a request to a clone it still needs.
func TestNoListingIsReadWhereNothingComparableIsOnRecord(t *testing.T) {
	h := newHarness(t)
	trees := h.readTrees()
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))

	// One round claims only what was queued; what it discovers waits for the next.
	h.run(10)

	if len(h.fetchedSince(0)) == 0 {
		t.Fatal("nothing was fetched, so the first commit was never asked about")
	}
	if n := trees.count(); n != 0 {
		t.Errorf("%d listings were read for a first commit: %v", n, trees.reads)
	}
}

func (h *harness) lockRows(commit string) []string {
	h.t.Helper()
	rows, err := h.store.DB().Query(`
		SELECT workflow_path || '|' || coverage FROM abom_lock_coverage
		 WHERE repository = 'gradle/actions' AND commit_sha = ? ORDER BY workflow_path`, commit)
	if err != nil {
		h.t.Fatalf("reading lock coverage: %v", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			h.t.Fatalf("reading lock coverage: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		h.t.Fatalf("reading lock coverage: %v", err)
	}
	return out
}

// A lock reading carries only where the digest covers what the reader reads. If the parser ever
// reads its lock from somewhere else, the directory checked is no longer the one that matters.
func TestTheLockDirectoryIsWhereTheParserReadsTheLock(t *testing.T) {
	if got := path.Dir(native.Path); got != lockDirectory {
		t.Errorf("the parser reads its lock from %s, and carrying checks %s", got, lockDirectory)
	}
}
