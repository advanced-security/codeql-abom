package expand

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// retainDatabases gives the expander a database cache that outlives a round, which is what a
// configured crawl has.
func (h *harness) retainDatabases() *DatabaseCache {
	h.t.Helper()
	c := &DatabaseCache{Dir: filepath.Join(h.t.TempDir(), "databases")}
	h.expander.Databases = c
	return c
}

func (h *harness) queriesRun() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.queried)
}

// builtFrom lists the trees that were extracted. The checkout path carries owner, repository,
// commit and filter, so a repeat in this list is a database built twice at one identity.
func (h *harness) builtFrom() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.built...)
}

// Building a database is the expensive step of a crawl, and throwing it away afterwards is what
// made running one more query across the organization cost a whole re-extraction.
func TestARoundRetainsTheDatabaseItBuiltAndRecordsWhereItPutIt(t *testing.T) {
	h := newHarness(t)
	h.retainDatabases()
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	held, err := h.store.RetainedDatabases()
	if err != nil {
		t.Fatalf("RetainedDatabases: %v", err)
	}
	if len(held) == 0 {
		t.Fatal("a round built a database and kept no record of one")
	}
	for _, d := range held {
		if _, err := os.Stat(d.Path); err != nil {
			t.Errorf("%s is on record at %s, which is not there: %v", d.Repository, d.Path, err)
		}
		if d.Commit == "" {
			t.Errorf("%s was recorded without the commit it was built from", d.Repository)
		}
		if d.Mode != store.AnalysisModeRegular {
			t.Errorf("%s was recorded in %q mode, want the shape the crawl actually builds",
				d.Repository, d.Mode)
		}
	}
}

// The index and the disk have to agree. A row naming a directory that is not there is a hit that
// cannot be served, and it would report a repository as analyzable when nothing can analyze it.
func TestEveryRecordedDatabaseIsTheOneOnDisk(t *testing.T) {
	h := newHarness(t)
	cache := h.retainDatabases()
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	held, err := h.store.RetainedDatabases()
	if err != nil {
		t.Fatalf("RetainedDatabases: %v", err)
	}
	for _, d := range held {
		served, ok := cache.Reuse(d.DatabaseIdentity)
		if !ok {
			t.Errorf("%s is recorded but the cache will not serve it back", d.Repository)
			continue
		}
		if served != d.Path {
			t.Errorf("%s is recorded at %q and served from %q", d.Repository, d.Path, served)
		}
	}
}

// Reuse skips extraction, never analysis. A database already on disk says nothing about whether
// a query has run against it, which is the ledger's call and stays where it was.
func TestADatabaseAlreadyOnDiskIsAnalyzedRatherThanExtractedAgain(t *testing.T) {
	h := newHarness(t)
	h.retainCheckouts()
	h.retainDatabases()
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	// One item, so the only repository the second round can pick up is the one just analyzed.
	// A full drain would discover dependencies and measure those instead.
	h.runWith(Options{Limit: 1})

	built, queried := h.databasesBuilt(), h.queriesRun()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued, so no second round would run")
	}
	h.runWith(Options{Limit: 1, QueryID: "abom/edges-v2"})

	if again := h.databasesBuilt() - built; again != 0 {
		t.Errorf("a second round extracted %d databases, want none: the commit is unchanged "+
			"and its database is on disk", again)
	}
	if ran := h.queriesRun() - queried; ran == 0 {
		t.Error("a retained database suppressed the analysis; reuse of a database is not a skip")
	}
}

// The same invariant over a whole crawl rather than one repository, because a fixture only covers
// the case somebody thought of. A repeat here is work the cache was supposed to have saved.
//
// Extracting one repository at several commits is not a repeat: a moving reference resolves
// somewhere new, and that is a different database answering a different question.
func TestNoTreeIsExtractedTwiceWhileItsDatabaseIsRetained(t *testing.T) {
	h := newHarness(t)
	h.retainCheckouts()
	h.retainDatabases()
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued, so no second round would run")
	}
	h.runWith(Options{Limit: 100, QueryID: "abom/edges-v2"})

	seen := map[string]int{}
	for _, tree := range h.builtFrom() {
		seen[tree]++
	}
	for tree, times := range seen {
		if times > 1 {
			t.Errorf("%s was extracted %d times; its database was retained after the first",
				tree, times)
		}
	}
}

// A failed build is the common case, not the rare one: 30 of them in twenty minutes of real
// crawling. Waiting for the abandoned-staging sweep to reclaim each is hours of occupancy the
// ceiling cannot account for, when the failure path knows exactly what to remove.
func TestAFailedBuildTakesItsStagedDatabaseWithIt(t *testing.T) {
	h := newHarness(t)
	cache := h.retainDatabases()
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.expander.Builder = failingBuilder{h}
	h.runWith(Options{Limit: 1})

	staged, err := os.ReadDir(filepath.Join(cache.Dir, ".staging"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading staging: %v", err)
	}
	if len(staged) != 0 {
		t.Errorf("a failed build left %d staged directories behind", len(staged))
	}
}

// failingBuilder records the attempt the way the harness does, then refuses.
type failingBuilder struct{ h *harness }

func (f failingBuilder) CreateDatabase(_ context.Context, srcDir, _, _ string) error {
	f.h.mu.Lock()
	f.h.built = append(f.h.built, srcDir)
	f.h.mu.Unlock()
	return errors.New("CodeQL detected code written in GitHub Actions but could not process it")
}

// Retention is configuration. Without it the round behaves as it always did, so nothing depends
// on a cache being present.
func TestWithoutACacheARoundKeepsNoDatabase(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	held, err := h.store.RetainedDatabases()
	if err != nil {
		t.Fatalf("RetainedDatabases: %v", err)
	}
	if len(held) != 0 {
		t.Errorf("a round with no cache recorded %d databases, want none", len(held))
	}
}

// A cache with no ceiling is a disk leak that presents as a crawl failure weeks later. The index
// and the disk have to come down together, or the survivor of the two is a lie about the other.
func TestTheCeilingEvictsDatabasesAndTakesTheirDirectoriesWithThem(t *testing.T) {
	h := newHarness(t)
	cache := h.retainDatabases()
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	before, err := h.store.RetainedDatabases()
	if err != nil {
		t.Fatalf("RetainedDatabases: %v", err)
	}
	if len(before) < 2 {
		t.Fatalf("the crawl retained %d databases, too few to evict from", len(before))
	}
	// A ceiling under what is held, so something has to go and something has to stay.
	total, err := h.store.RetainedDatabaseBytes()
	if err != nil {
		t.Fatalf("RetainedDatabaseBytes: %v", err)
	}
	cache.MaxBytes = total / 2

	dropped, err := h.expander.trimDatabases()
	if err != nil {
		t.Fatalf("trimDatabases: %v", err)
	}
	if dropped == 0 {
		t.Fatal("nothing was evicted with the ceiling set below what is held")
	}

	after, err := h.store.RetainedDatabaseBytes()
	if err != nil {
		t.Fatalf("RetainedDatabaseBytes: %v", err)
	}
	if after > cache.MaxBytes {
		t.Errorf("the cache holds %d bytes against a ceiling of %d", after, cache.MaxBytes)
	}

	// Whatever survived has to still be there: evicting is not permission to lose the rest.
	survivors, err := h.store.RetainedDatabases()
	if err != nil {
		t.Fatalf("RetainedDatabases: %v", err)
	}
	if len(survivors) == 0 {
		t.Fatal("eviction emptied the cache rather than bringing it inside the ceiling")
	}
	for _, d := range survivors {
		if _, ok := cache.Reuse(d.DatabaseIdentity); !ok {
			t.Errorf("%s survived eviction on record but not on disk", d.Repository)
		}
	}
}
