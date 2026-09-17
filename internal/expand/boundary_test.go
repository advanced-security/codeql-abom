package expand

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// The corpus this exists for: 204 repositories analyzed, 0 owned, 56,830 nodes, and the one
// declared thing anywhere sat unanalyzed at depth 2 behind somebody else's supply chain. Nothing
// stopped any of it, because Enqueue drops a candidate only on depth and "names no repository".
//
// The sweep is where it showed worst: it re-applied the discovery rule to every repository in the
// table, including the 895 upstream ones nothing owned reaches.
func TestTheSweepDoesNotReachOutsideTheClosure(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()
	// An upstream repository with dependencies of its own that nothing owned reaches.
	h.referencedFromSeed("stranger/repo", "stranger/only-they-use-this")

	before := h.pendingCount()
	h.sweep()

	if n := countRows(t, h.store,
		`SELECT COUNT(*) FROM abom_frontier WHERE node_key = 'stranger/only-they-use-this'`); n != 0 {
		t.Error("a dependency reached only from outside the declared scope was queued")
	}
	if after := h.pendingCount(); after != before {
		t.Errorf("the queue grew from %d to %d without an owned route", before, after)
	}
}

// The mirror image, and the one that must keep working: a dependency of something owned is the
// supply chain this tool exists to describe, however deep it sits.
func TestADiscoveryReachedFromTheDeclaredSetIsQueued(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	if n := countRows(t, h.store,
		`SELECT COUNT(*) FROM abom_frontier WHERE discovered_from = 'gradle/actions'`); n == 0 {
		t.Error("gradle is declared, so what its repositories name is in scope")
	}
}

// With nothing declared the closure is empty, and an empty closure admits nothing. Admitting
// everything instead would make the boundary inert exactly when it has never been configured,
// which is the state a first run is in.
func TestWithNothingDeclaredNothingIsDiscovered(t *testing.T) {
	h := newHarness(t)
	h.enqueueUnowned(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	if n := countRows(t, h.store,
		`SELECT COUNT(*) FROM abom_frontier WHERE discovered_from <> ''`); n != 0 {
		t.Errorf("%d dependencies were discovered with no declared scope", n)
	}
}

// Seeding is the same question asked before anything has been analyzed: the roots are what is
// declared, not what happens to carry `origin='seed'`. The two were assumed to be the same set
// and are not — 0 of the reference corpus's 204 seed repositories belong to a declared account.
func TestSeedingStartsFromWhatIsDeclaredNotFromOrigin(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()
	h.referencedFromSeed("stranger/repo", "stranger/only-they-use-this")

	if _, err := SeedFrontier(h.store, h.queue); err != nil {
		t.Fatalf("SeedFrontier: %v", err)
	}

	if n := countRows(t, h.store,
		`SELECT COUNT(*) FROM abom_frontier WHERE node_key = 'stranger/only-they-use-this'`); n != 0 {
		t.Error("seeding reached a repository outside the declared scope")
	}
}

// The owned tier was added to `tierOf`, to `abom_tier_suite` and to the Discovery page, and never
// to `plansByTier` — which builds plans for direct and transitive only. A batch landing in it
// therefore gets a zero-value plan, which `pendingWork` reads as nothing outstanding, so the
// repository is settled as *already recorded* without ever being analyzed.
//
// Latent until now: the owned tier held accounts the corpus contained nothing from. After this
// plan every root is owned, so it would have silently swallowed the entire root set.
func TestAnOwnedRepositoryIsAnalyzedRatherThanSettledUnread(t *testing.T) {
	h := newHarness(t)
	declareScope(t, h.store, "gradle")
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))

	stats := h.run(100)

	if stats.Processed == 0 {
		t.Fatalf("nothing was analyzed: %+v", stats)
	}
	if n := countRows(t, h.store, `SELECT COUNT(*) FROM abom_query_run`); n == 0 {
		t.Error("an owned repository was settled without a query ever running against it")
	}
}

func declareScope(t *testing.T, s *store.Store, owners ...string) {
	t.Helper()
	accounts := make([]store.DeclaredAccount, 0, len(owners))
	for _, o := range owners {
		accounts = append(accounts, store.DeclaredAccount{
			Owner: o, Kind: store.AccountOrganization, Enabled: true,
		})
	}
	if err := s.SetOwnedScope(accounts, store.DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
}

func (h *harness) pendingCount() int {
	h.t.Helper()
	return countRows(h.t, h.store, `SELECT COUNT(*) FROM abom_frontier WHERE status = 'pending'`)
}

func (h *harness) sweep() int {
	h.t.Helper()
	n, err := SweepDiscoveries(h.store, h.queue)
	if err != nil {
		h.t.Fatalf("SweepDiscoveries: %v", err)
	}
	return n
}

var _ = frontier.StatusPending
