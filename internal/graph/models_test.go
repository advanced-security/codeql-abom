package graph

import (
	"reflect"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// everyOrigin lists upstream rows too, so a write that only an upstream repository sees still shows.
var everyOrigin = UsesQuery{Origins: []string{store.OriginSeed, store.OriginUpstream}, Limit: 1000}

// drillDown is what the details page asks of one node: its uses and its versions.
type drillDown struct {
	Uses     UsesPage
	Versions VersionsPage
}

func askDrillDown(t *testing.T, b *Builder, key string) drillDown {
	t.Helper()
	uses, err := b.UsesOf(key, everyOrigin)
	if err != nil {
		t.Fatalf("UsesOf(%q): %v", key, err)
	}
	versions, err := b.VersionsOf(key, everyOrigin)
	if err != nil {
		t.Fatalf("VersionsOf(%q): %v", key, err)
	}
	return drillDown{Uses: uses, Versions: versions}
}

// The page asks uses and versions of a node together, and each loaded the whole graph: 0.27 to
// 0.60 s on the crawled organization, nearly all of it the load, with nothing written in between.
func TestTheDrillDownLoadsTheGraphOncePerDatabaseState(t *testing.T) {
	s := fixture(t)
	models := &Models{}
	b := &Builder{Store: s, Models: models}

	first := askDrillDown(t, b, "actions/checkout")
	again := askDrillDown(t, &Builder{Store: s, Models: models}, "actions/checkout")
	askDrillDown(t, b, "octo-org/shared/.github/workflows/build.yml")

	if got := models.Loads(); got != 1 {
		t.Errorf("six walks over one database state loaded the graph %d times, want once", got)
	}
	if !reflect.DeepEqual(first, again) {
		t.Errorf("the shared graph answered %+v, then %+v", first, again)
	}
	if len(first.Uses.Items) == 0 {
		t.Fatal("actions/checkout has no uses, so sharing the graph proved nothing")
	}
}

// A loaded graph is an answer about the database as it was. Most of what the walk reads changes
// without a new generation: the queue resolves a tag mid-cycle, a declaration promotes a repository,
// ingestion lands edges before the cycle records one, a lock is read during analysis, and another
// process may write the file. Any of them must retire the graph, and the next answer must be the one
// a fresh load gives.
func TestAnyCommitRetiresTheLoadedGraph(t *testing.T) {
	const resolved = "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00"
	resolveCheckout := func(t *testing.T, db *store.Store) {
		t.Helper()
		// In place, as the queue completes an item: no row is added and no generation recorded.
		if _, err := db.DB().Exec(`UPDATE abom_frontier SET status = 'done', resolved_sha = ?
			WHERE node_key = 'actions/checkout' AND ref = ''`, resolved); err != nil {
			t.Fatal(err)
		}
	}
	shared := func(t *testing.T) *store.Store {
		t.Helper()
		s := fixture(t)
		upstreamUser(t, s, "elsewhere/consumer", "actions/checkout")
		if _, err := s.DB().Exec(`INSERT INTO abom_frontier (node_key, ref, owner, repo, depth,
			status, enqueued_at) VALUES ('actions/checkout', '', 'actions', 'checkout', 1, 'pending',
			'2026-10-01T00:00:00Z')`); err != nil {
			t.Fatal(err)
		}
		return s
	}

	for name, c := range map[string]struct {
		base  func(*testing.T) *store.Store
		key   string
		write func(*testing.T, *store.Store)
		// moves says the write changes the answer, so a stale graph would be caught by its content
		// and not only by its load count.
		moves bool
	}{
		"a new generation": {shared, "actions/checkout", func(t *testing.T, s *store.Store) {
			if _, err := s.RecordGraphGeneration(store.GraphRebuild{}); err != nil {
				t.Fatal(err)
			}
		}, false},
		"a reference resolving without a generation": {shared, "actions/checkout", resolveCheckout, true},
		"a repository promoted without a generation": {shared, "actions/checkout",
			func(t *testing.T, s *store.Store) {
				if _, err := s.InsertOwnedRepository("elsewhere/consumer"); err != nil {
					t.Fatal(err)
				}
			}, true},
		"an edge ingested without a generation": {shared, "actions/checkout",
			func(t *testing.T, s *store.Store) { upstreamUser(t, s, "elsewhere/another", "actions/checkout") },
			true},
		"a lock read without a generation": {lockedVendorFixture, "vendor/action",
			func(t *testing.T, s *store.Store) {
				if err := s.RecordLockReading("octo/unlocked", unlockedRoot, "test-reader",
					[]store.LockCoverage{{WorkflowPath: ".github/workflows/ci.yml", Coverage: "covered",
						Schema: "v0.0.2"}},
					store.LockGraph{
						Dependencies: []store.LockDependency{{Pin: "vendor/action@v1", Owner: "vendor",
							Repo: "action", Ref: "v1", Algorithm: "sha1", Digest: vendorLocked}},
						WorkflowPins: []store.LockWorkflowPin{
							{WorkflowPath: ".github/workflows/ci.yml", Pin: "vendor/action@v1"}},
					}); err != nil {
					t.Fatal(err)
				}
			}, true},
		"a write from another connection": {shared, "actions/checkout",
			func(t *testing.T, s *store.Store) {
				var path string
				if err := s.DB().QueryRow(
					`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
					t.Fatal(err)
				}
				other, err := store.Open(path, store.Options{})
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				resolveCheckout(t, other)
			}, true},
	} {
		t.Run(name, func(t *testing.T) {
			s := c.base(t)
			models := &Models{}
			cached := &Builder{Store: s, Models: models}
			before := askDrillDown(t, cached, c.key)

			c.write(t, s)
			after := askDrillDown(t, cached, c.key)

			if got := models.Loads(); got != 2 {
				t.Errorf("the graph was loaded %d times across the write, want once on each side", got)
			}
			if fresh := askDrillDown(t, &Builder{Store: s}, c.key); !reflect.DeepEqual(after, fresh) {
				t.Errorf("after the write the shared graph answered\n%+v\na fresh load answers\n%+v",
					after, fresh)
			}
			if moved := !reflect.DeepEqual(before, after); moved != c.moves {
				t.Errorf("the write moved the answer: %v, want %v", moved, c.moves)
			}
		})
	}
}

// The page asks uses and versions at once, so readers meet on one graph. It is shared, which is
// only safe because a walk never writes to it; -race is what holds that.
func TestConcurrentReadersShareOneLoadedGraph(t *testing.T) {
	s := fixture(t)
	want := askDrillDown(t, &Builder{Store: s}, "actions/checkout")
	models := &Models{}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := &Builder{Store: s, Models: models}
			uses, err := b.UsesOf("actions/checkout", everyOrigin)
			if err != nil {
				t.Errorf("UsesOf: %v", err)
				return
			}
			versions, err := b.VersionsOf("actions/checkout", everyOrigin)
			if err != nil {
				t.Errorf("VersionsOf: %v", err)
				return
			}
			if got := (drillDown{Uses: uses, Versions: versions}); !reflect.DeepEqual(got, want) {
				t.Errorf("a concurrent reader answered %+v, want %+v", got, want)
			}
		}()
	}
	wg.Wait()

	if got := models.Loads(); got != 1 {
		t.Errorf("eight concurrent readers loaded the graph %d times, want once", got)
	}
}
