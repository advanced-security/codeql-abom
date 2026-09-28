package expand

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func regularAt(repo, sha, filter string) store.DatabaseIdentity {
	return store.DatabaseIdentity{
		Repository: repo, Commit: sha, SourceFilter: filter, Mode: store.AnalysisModeRegular,
	}
}

// buildInto stages a database, writes something the size of one into it, and promotes it.
func buildInto(t *testing.T, c *DatabaseCache, id store.DatabaseIdentity, size int) string {
	t.Helper()
	return buildBy(t, c, id, size, cacheCLI)
}

// cacheCLI is the CodeQL the cache tests' databases say built them.
const cacheCLI = "2.27.1"

// buildBy promotes a database of about size bytes that records cli as the CodeQL that built it.
func buildBy(t *testing.T, c *DatabaseCache, id store.DatabaseIdentity, size int, cli string) string {
	t.Helper()
	staged, dbDir, err := c.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("building into %s: %v", dbDir, err)
	}
	body := []byte("creationMetadata:\n  cliVersion: " + cli + "\n")
	if len(body) < size {
		body = append(body, make([]byte, size-len(body))...)
	}
	if err := os.WriteFile(filepath.Join(dbDir, "codeql-database.yml"), body, 0o600); err != nil {
		t.Fatalf("writing a database: %v", err)
	}
	path, _, err := c.Retain(staged, id)
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	return path
}

// Extraction changes between releases, and a result names the CodeQL that produced it. A database
// another CodeQL built would put that release's extraction under this one's name.
func TestADatabaseIsServedOnlyToTheCodeQLThatBuiltIt(t *testing.T) {
	cache := &DatabaseCache{Dir: t.TempDir()}
	id := regularAt("octo/app", "abc123", "f0")
	buildBy(t, cache, id, 1024, "2.27.0")

	if _, ok := cache.Reuse(id, "2.27.1"); ok {
		t.Error("a database CodeQL 2.27.0 built was served to 2.27.1")
	}
	if _, ok := cache.Reuse(id, "2.27.0"); !ok {
		t.Error("a database was not served back to the CodeQL that built it")
	}
}

// Retention is the point: a database built once must answer the next round's question without
// being extracted again.
func TestARetainedDatabaseIsServedOnTheNextRound(t *testing.T) {
	cache := &DatabaseCache{Dir: t.TempDir()}
	id := regularAt("octo/app", "abc123", "f0")
	built := buildInto(t, cache, id, 2048)

	served, ok := cache.Reuse(id, cacheCLI)
	if !ok {
		t.Fatal("a database promoted a moment ago is not served back")
	}
	if served != built {
		t.Errorf("served %q, want the database that was built at %q", served, built)
	}
	if _, err := os.Stat(filepath.Join(served, "codeql-database.yml")); err != nil {
		t.Errorf("the served path does not hold a database: %v", err)
	}
}

// A commit alone does not name a tree. A database built from narrower paths is missing files, and
// downstream that is indistinguishable from a repository that does not have them.
func TestADatabaseBuiltUnderOneFilterDoesNotServeAnother(t *testing.T) {
	cache := &DatabaseCache{Dir: t.TempDir()}
	buildInto(t, cache, regularAt("octo/app", "abc123", "f0"), 2048)

	if _, ok := cache.Reuse(regularAt("octo/app", "abc123", "wider"), cacheCLI); ok {
		t.Error("a database built under one filter was served for another")
	}
}

// The same rule id means opposite things in the two shapes, so they cannot share a directory.
func TestTheTwoAnalysisModesOfOneCommitAreSeparateDatabases(t *testing.T) {
	cache := &DatabaseCache{Dir: t.TempDir()}
	regular := regularAt("octo/app", "abc123", "f0")
	extended := regular
	extended.Mode = store.AnalysisModeExtended

	first := buildInto(t, cache, regular, 1024)
	second := buildInto(t, cache, extended, 1024)

	if first == second {
		t.Fatalf("both shapes were built at %q; one has overwritten the other", first)
	}
	for _, id := range []store.DatabaseIdentity{regular, extended} {
		if _, ok := cache.Reuse(id, cacheCLI); !ok {
			t.Errorf("the %s database is not served after the other was built", id.Mode)
		}
	}
}

// A build that never finished must not be reachable. Serving a partial database would report a
// repository as analyzed and clean when nothing analyzed it.
func TestAnInterruptedBuildIsNeverServedAsAFinishedDatabase(t *testing.T) {
	cache := &DatabaseCache{Dir: t.TempDir()}
	id := regularAt("octo/app", "abc123", "f0")

	_, dbDir, err := cache.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("building into %s: %v", dbDir, err)
	}

	if _, ok := cache.Reuse(id, cacheCLI); ok {
		t.Error("a staged build that was never promoted was served as a database")
	}
}

// Eviction works from the measured size, so the measurement has to be real rather than declared.
func TestRetainReportsWhatTheDatabaseOccupies(t *testing.T) {
	cache := &DatabaseCache{Dir: t.TempDir()}
	staged, dbDir, err := cache.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("building into %s: %v", dbDir, err)
	}
	if err := os.WriteFile(
		filepath.Join(dbDir, "codeql-database.yml"), make([]byte, 4096), 0o600); err != nil {
		t.Fatalf("writing a database: %v", err)
	}

	_, bytes, err := cache.Retain(staged, regularAt("octo/app", "abc123", "f0"))
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	if bytes != 4096 {
		t.Errorf("Retain measured %d bytes, want the 4096 actually on disk", bytes)
	}
}

// Owner and repository names arrive from somebody else's workflow file, so they are input. A
// separator or a parent reference in one would write outside the cache.
func TestARepositoryNameThatWouldEscapeTheCacheIsRefused(t *testing.T) {
	cache := &DatabaseCache{Dir: t.TempDir()}
	for _, repo := range []string{"../etc/app", "octo/../../etc", "octo", "", "octo/app/extra"} {
		staged, dbDir, err := cache.Stage()
		if err != nil {
			t.Fatalf("Stage: %v", err)
		}
		if err := os.MkdirAll(dbDir, 0o755); err != nil {
			t.Fatalf("building into %s: %v", dbDir, err)
		}
		if _, _, err := cache.Retain(staged, regularAt(repo, "abc123", "f0")); err == nil {
			t.Errorf("a database for repository %q was retained; it names no safe path", repo)
		}
	}
}

// The source filter is an *id*, not the pattern set it stands for. `filterID` hashes the
// patterns, so `.github/**` never reaches a path segment — and if a caller ever passed the
// patterns themselves, the entry is refused rather than writing somewhere unintended.
func TestASourceFilterThatIsAPatternRatherThanAnIdIsRefused(t *testing.T) {
	cache := &DatabaseCache{Dir: t.TempDir()}

	held, err := filepath.Abs(cache.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, filter := range []string{".github/**", "../escape", "a/b"} {
		staged, dbDir, err := cache.Stage()
		if err != nil {
			t.Fatalf("Stage: %v", err)
		}
		if err := os.MkdirAll(dbDir, 0o755); err != nil {
			t.Fatalf("building into %s: %v", dbDir, err)
		}
		path, _, err := cache.Retain(staged, regularAt("octo/app", "abc123", filter))
		if err == nil {
			t.Errorf("a filter of %q was retained at %q; it names no safe segment", filter, path)
		}
	}

	// And the id a real round produces is accepted, or this test would pass by refusing
	// everything.
	staged, dbDir, err := cache.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("building into %s: %v", dbDir, err)
	}
	path, _, err := cache.Retain(staged, regularAt("octo/app", "abc123", filterID(sparsePatterns)))
	if err != nil {
		t.Fatalf("the filter id a round produces was refused: %v", err)
	}
	if !strings.HasPrefix(path, held) {
		t.Errorf("retained at %q, which is outside %q", path, held)
	}
}

// Discarding is how eviction reclaims disk, and it must refuse a path outside the cache: the
// store hands back whatever was recorded, and a wrong row must not become a wrong deletion.
func TestDiscardingRefusesAPathOutsideTheCache(t *testing.T) {
	cache := &DatabaseCache{Dir: t.TempDir()}
	outside := filepath.Join(t.TempDir(), "somebody-elses-work")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("preparing %s: %v", outside, err)
	}

	if err := cache.Discard(outside); err == nil {
		t.Error("a path outside the cache was discarded")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the directory outside the cache was removed anyway: %v", err)
	}
}

// Eviction has to actually free the disk, or the budget means nothing.
func TestDiscardingRemovesTheDatabaseAndTheEntryHoldingIt(t *testing.T) {
	cache := &DatabaseCache{Dir: t.TempDir()}
	id := regularAt("octo/app", "abc123", "f0")
	built := buildInto(t, cache, id, 2048)

	if err := cache.Discard(built); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if _, ok := cache.Reuse(id, cacheCLI); ok {
		t.Error("a discarded database is still served")
	}
	if _, err := os.Stat(built); !os.IsNotExist(err) {
		t.Errorf("the database directory survived being discarded: %v", err)
	}
}

// A crawl that was killed mid-build leaves a staged database nothing will ever promote. Left
// there it is occupancy the ceiling cannot see, because the ceiling is computed from the rows
// that name promoted databases -- which is exactly the leak a ceiling exists to stop.
func TestAnAbandonedStagedBuildIsReclaimed(t *testing.T) {
	cache := &DatabaseCache{Dir: t.TempDir()}
	staged, dbDir, err := cache.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("building into %s: %v", dbDir, err)
	}
	old := time.Now().Add(-2 * stagingLifetime)
	if err := os.Chtimes(staged, old, old); err != nil {
		t.Fatalf("aging the staged build: %v", err)
	}

	reclaimed, err := cache.ReclaimStaging()
	if err != nil {
		t.Fatalf("ReclaimStaging: %v", err)
	}
	if reclaimed != 1 {
		t.Errorf("reclaimed %d abandoned builds, want 1", reclaimed)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Errorf("the abandoned staged build survived: %v", err)
	}
}

// A build still in flight is not abandoned. Reclaiming it would delete the database out from
// under the extractor writing it.
func TestAStagedBuildInFlightIsNotReclaimed(t *testing.T) {
	cache := &DatabaseCache{Dir: t.TempDir()}
	staged, dbDir, err := cache.Stage()
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("building into %s: %v", dbDir, err)
	}

	reclaimed, err := cache.ReclaimStaging()
	if err != nil {
		t.Fatalf("ReclaimStaging: %v", err)
	}
	if reclaimed != 0 {
		t.Errorf("reclaimed %d builds that are still in flight, want 0", reclaimed)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Errorf("a build in flight was removed: %v", err)
	}
}

// A nil cache is a disabled one. Every method tolerates it, so nothing has to branch on whether
// retention is configured.
func TestADisabledCacheServesNothingAndRefusesToStage(t *testing.T) {
	var cache *DatabaseCache
	if _, ok := cache.Reuse(regularAt("octo/app", "abc123", "f0"), cacheCLI); ok {
		t.Error("a disabled cache served a database")
	}
	if _, _, err := cache.Stage(); err == nil {
		t.Error("a disabled cache staged a build")
	}
	if err := cache.Discard("/anything"); err != nil {
		t.Errorf("discarding against a disabled cache failed: %v", err)
	}
}
