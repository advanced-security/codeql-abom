package store

import (
	"path/filepath"
	"testing"
)

func cacheStore(t *testing.T) *Store {
	t.Helper()
	s, err := openTestStore(t, filepath.Join(t.TempDir(), "abom.db"), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func held(t *testing.T, s *Store, repo, sha string, bytes int64) DatabaseIdentity {
	t.Helper()
	id := DatabaseIdentity{
		Repository: repo, Commit: sha, SourceFilter: ".github/**", Mode: AnalysisModeRegular,
	}
	if _, err := s.RetainDatabase(RetainedDatabase{
		DatabaseIdentity: id,
		Path:             filepath.Join("/cache", repo, sha),
		Bytes:            bytes,
		CLIVersion:       "2.24.1",
	}); err != nil {
		t.Fatalf("RetainDatabase(%s@%s): %v", repo, sha, err)
	}
	return id
}

// Retention is the whole point of building a database: a corpus that already exists is what makes
// running one query against many repositories cheap enough to do repeatedly.
func TestARetainedDatabaseIsFoundAgainByTheIdentityThatBuiltIt(t *testing.T) {
	s := cacheStore(t)
	id := held(t, s, "octo/app", "sha-one", 4096)

	found, ok, err := s.RetainedDatabaseAt(id)
	if err != nil {
		t.Fatalf("RetainedDatabaseAt: %v", err)
	}
	if !ok {
		t.Fatal("a database retained a moment ago is not offered back")
	}
	if found.Path != filepath.Join("/cache", "octo/app", "sha-one") {
		t.Errorf("retained at %q, want the path it was built at", found.Path)
	}
	if found.Bytes != 4096 {
		t.Errorf("retained %d bytes, want 4096", found.Bytes)
	}
}

// A commit alone does not name a tree: only the paths that were fetched decide what the extractor
// saw. A hit under the wrong filter costs correctness rather than time, which is the same rule the
// checkout cache already holds one level down.
func TestADatabaseBuiltUnderOneFilterIsNotOfferedUnderAnother(t *testing.T) {
	s := cacheStore(t)
	held(t, s, "octo/app", "sha-one", 4096)

	_, ok, err := s.RetainedDatabaseAt(DatabaseIdentity{
		Repository: "octo/app", Commit: "sha-one",
		SourceFilter: "**", Mode: AnalysisModeRegular,
	})
	if err != nil {
		t.Fatalf("RetainedDatabaseAt: %v", err)
	}
	if ok {
		t.Error("a database built from a narrow checkout answered a request for the whole tree")
	}
}

// The same rule id means opposite things in the two shapes, so one commit has two databases and
// neither may stand in for the other.
func TestOneCommitHoldsADifferentDatabaseInEachAnalysisMode(t *testing.T) {
	s := cacheStore(t)
	regular := DatabaseIdentity{
		Repository: "octo/app", Commit: "sha-one",
		SourceFilter: ".github/**", Mode: AnalysisModeRegular,
	}
	extended := regular
	extended.Mode = AnalysisModeExtended

	for _, id := range []DatabaseIdentity{regular, extended} {
		if _, err := s.RetainDatabase(RetainedDatabase{
			DatabaseIdentity: id, Path: "/cache/" + id.Mode, Bytes: 1000,
		}); err != nil {
			t.Fatalf("RetainDatabase(%s): %v", id.Mode, err)
		}
	}

	for _, id := range []DatabaseIdentity{regular, extended} {
		found, ok, err := s.RetainedDatabaseAt(id)
		if err != nil {
			t.Fatalf("RetainedDatabaseAt(%s): %v", id.Mode, err)
		}
		if !ok {
			t.Fatalf("the %s database for one commit was overwritten by the other", id.Mode)
		}
		if found.Path != "/cache/"+id.Mode {
			t.Errorf("%s mode resolved to %q, want the database built in that shape",
				id.Mode, found.Path)
		}
	}
}

// A writer that did not state a shape leaves a row that cannot be told apart from one that did,
// and a wrong value in it would be indistinguishable from a right one.
func TestADatabaseWithoutAStatedShapeIsRefused(t *testing.T) {
	s := cacheStore(t)
	if _, err := s.RetainDatabase(RetainedDatabase{
		DatabaseIdentity: DatabaseIdentity{Repository: "octo/app", Commit: "sha-one"},
		Path:             "/cache/octo/app",
		Bytes:            1,
	}); err == nil {
		t.Error("a database recorded without an analysis mode was accepted")
	}
}

// Rebuilding at one identity replaces the row, or the budget would be computed from databases
// that no longer exist and eviction would free nothing.
func TestRebuildingADatabaseReplacesTheRowRatherThanAddingOne(t *testing.T) {
	s := cacheStore(t)
	held(t, s, "octo/app", "sha-one", 4096)
	held(t, s, "octo/app", "sha-one", 8192)

	all, err := s.RetainedDatabases()
	if err != nil {
		t.Fatalf("RetainedDatabases: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("one identity produced %d rows, want 1", len(all))
	}
	if all[0].Bytes != 8192 {
		t.Errorf("the retained size is %d, want the rebuild's 8192", all[0].Bytes)
	}
}

// Recency decides what eviction takes, so a database a crawl is reading must not be the one
// dropped just because it was built first.
func TestUsingADatabaseMovesItOutOfTheEvictionQueue(t *testing.T) {
	s := cacheStore(t)
	first := held(t, s, "octo/app", "sha-one", 1000)
	held(t, s, "octo/lib", "sha-two", 1000)

	if _, err := s.UseDatabase(first); err != nil {
		t.Fatalf("UseDatabase: %v", err)
	}

	dropped, err := s.EvictDatabasesOver(1000)
	if err != nil {
		t.Fatalf("EvictDatabasesOver: %v", err)
	}
	if len(dropped) != 1 {
		t.Fatalf("eviction dropped %d databases, want 1", len(dropped))
	}
	if dropped[0].Repository != "octo/lib" {
		t.Errorf("eviction took %s, want the one nothing has used since it was built",
			dropped[0].Repository)
	}
}

// An evicted database is a named absence. A run that cannot analyze a repository has to be able to
// say it was evicted, rather than reporting a repository with no results.
func TestEvictionNamesEveryDatabaseItDropped(t *testing.T) {
	s := cacheStore(t)
	held(t, s, "octo/app", "sha-one", 1000)
	held(t, s, "octo/lib", "sha-two", 1000)
	held(t, s, "octo/tool", "sha-three", 1000)

	dropped, err := s.EvictDatabasesOver(1000)
	if err != nil {
		t.Fatalf("EvictDatabasesOver: %v", err)
	}
	if len(dropped) != 2 {
		t.Fatalf("eviction dropped %d databases, want 2", len(dropped))
	}
	for _, d := range dropped {
		if d.Path == "" || d.Repository == "" {
			t.Errorf("eviction reported %+v, which names nothing a caller could remove", d)
		}
		if _, ok, err := s.RetainedDatabaseAt(d.DatabaseIdentity); err != nil || ok {
			t.Errorf("%s was reported as evicted and is still on record", d.Repository)
		}
	}
}

// Eviction frees what the budget asks for and stops. Taking more would throw away work whose only
// cost was already paid, which is the opposite of what retaining databases is for.
func TestEvictionStopsAsSoonAsTheBudgetIsMet(t *testing.T) {
	s := cacheStore(t)
	held(t, s, "octo/app", "sha-one", 1000)
	held(t, s, "octo/lib", "sha-two", 1000)
	held(t, s, "octo/tool", "sha-three", 1000)

	if _, err := s.EvictDatabasesOver(2000); err != nil {
		t.Fatalf("EvictDatabasesOver: %v", err)
	}

	total, err := s.RetainedDatabaseBytes()
	if err != nil {
		t.Fatalf("RetainedDatabaseBytes: %v", err)
	}
	if total != 2000 {
		t.Errorf("eviction left %d bytes, want exactly the 2000 the budget allows", total)
	}
}

// A cache inside its budget is not asked to give anything up, and an empty one reports no bytes
// rather than failing on an aggregate over no rows.
func TestACacheInsideItsBudgetGivesNothingUp(t *testing.T) {
	s := cacheStore(t)

	total, err := s.RetainedDatabaseBytes()
	if err != nil {
		t.Fatalf("RetainedDatabaseBytes on an empty cache: %v", err)
	}
	if total != 0 {
		t.Errorf("an empty cache reports %d bytes, want 0", total)
	}

	held(t, s, "octo/app", "sha-one", 1000)
	dropped, err := s.EvictDatabasesOver(5000)
	if err != nil {
		t.Fatalf("EvictDatabasesOver: %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("eviction dropped %d databases while inside the budget, want 0", len(dropped))
	}
}
