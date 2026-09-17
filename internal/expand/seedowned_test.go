package expand

import (
	"strings"
	"testing"
)

// SeedFrontier reads edges, so it can only queue the dependencies of a repository that has
// already been analyzed. Nothing ever analyzed the operator's own repositories: on the reference
// corpus 198 of them sat in the corpus as roots with an empty queue and an empty database
// catalog, and the crawl ran entirely on other people's code.
func TestOwnedRepositoriesAreQueuedForAnalysis(t *testing.T) {
	h := newSeedHarness(t)
	h.declare("acme/service")
	if _, err := h.store.InsertOwnedRepository("acme/service"); err != nil {
		t.Fatalf("InsertOwnedRepository: %v", err)
	}

	n, err := SeedOwnedRepositories(h.store, h.queue)
	if err != nil {
		t.Fatalf("SeedOwnedRepositories: %v", err)
	}

	if n != 1 {
		t.Errorf("queued %d, want the declared repository queued", n)
	}
	if got := h.queued(); len(got) != 1 || got[0].NodeKey != "acme/service" {
		t.Errorf("queue holds %+v, want acme/service", got)
	}
}

// A repository the operator owns is the origin of its own report, so it precedes every dependency
// in the claim order. Claiming is `depth ASC`, and queuing an owned repository at depth 1 would
// put it behind the whole first hop of somebody else's supply chain.
func TestAnOwnedRepositoryIsQueuedAtDepthZero(t *testing.T) {
	h := newSeedHarness(t)
	h.declare("acme/service")
	if _, err := h.store.InsertOwnedRepository("acme/service"); err != nil {
		t.Fatalf("InsertOwnedRepository: %v", err)
	}

	if _, err := SeedOwnedRepositories(h.store, h.queue); err != nil {
		t.Fatalf("SeedOwnedRepositories: %v", err)
	}

	if got := h.queued(); len(got) != 1 || got[0].Depth != 0 {
		t.Errorf("queued at depth %d, want 0", got[0].Depth)
	}
}

// Analysis batches by `owner/repo@ref`, so an item with no owner or repo is a checkout nobody can
// perform and it would sit in the queue failing forever.
func TestAQueuedOwnedRepositoryCarriesItsOwnerAndRepo(t *testing.T) {
	h := newSeedHarness(t)
	h.declare("acme/service")
	if _, err := h.store.InsertOwnedRepository("acme/service"); err != nil {
		t.Fatalf("InsertOwnedRepository: %v", err)
	}

	if _, err := SeedOwnedRepositories(h.store, h.queue); err != nil {
		t.Fatalf("SeedOwnedRepositories: %v", err)
	}

	it := h.queued()[0]
	if it.Owner != "acme" || it.Repo != "service" {
		t.Errorf("owner/repo = %q/%q, want acme/service", it.Owner, it.Repo)
	}
}

// A repository outside the declared scope is not the operator's to analyze. Queuing it would spend
// their machine on somebody else's code and put it in their bill of materials as a root.
func TestAnUndeclaredRepositoryIsNotQueued(t *testing.T) {
	h := newSeedHarness(t)
	h.declare("acme/service")
	if _, err := h.store.InsertOwnedRepository("acme/service"); err != nil {
		t.Fatalf("InsertOwnedRepository: %v", err)
	}
	if _, err := h.store.InsertUpstreamRepository("stranger/thing"); err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}

	if _, err := SeedOwnedRepositories(h.store, h.queue); err != nil {
		t.Fatalf("SeedOwnedRepositories: %v", err)
	}

	for _, it := range h.queued() {
		if strings.HasPrefix(it.NodeKey, "stranger/") {
			t.Errorf("queued %q, which nobody declared", it.NodeKey)
		}
	}
}

// Seeding runs on every start. A second pass must not re-queue a repository that has already been
// analyzed, or the crawl re-analyzes the whole estate whenever the server restarts.
func TestSeedingTwiceDoesNotQueueTheSameRepositoryAgain(t *testing.T) {
	h := newSeedHarness(t)
	h.declare("acme/service")
	if _, err := h.store.InsertOwnedRepository("acme/service"); err != nil {
		t.Fatalf("InsertOwnedRepository: %v", err)
	}
	if _, err := SeedOwnedRepositories(h.store, h.queue); err != nil {
		t.Fatalf("first SeedOwnedRepositories: %v", err)
	}

	again, err := SeedOwnedRepositories(h.store, h.queue)
	if err != nil {
		t.Fatalf("second SeedOwnedRepositories: %v", err)
	}

	if again != 0 {
		t.Errorf("queued %d on the second pass, want 0", again)
	}
	if got := h.queued(); len(got) != 1 {
		t.Errorf("queue holds %d rows, want 1", len(got))
	}
}

// An owned repository is queued because the operator owns it, not because anything referenced it.
// Naming a referrer would invent a dependency that does not exist.
func TestAnOwnedRepositoryIsNotAttributedToAReferrer(t *testing.T) {
	h := newSeedHarness(t)
	h.declare("acme/service")
	if _, err := h.store.InsertOwnedRepository("acme/service"); err != nil {
		t.Fatalf("InsertOwnedRepository: %v", err)
	}

	if _, err := SeedOwnedRepositories(h.store, h.queue); err != nil {
		t.Fatalf("SeedOwnedRepositories: %v", err)
	}

	var from string
	if err := h.store.DB().QueryRow(
		`SELECT COALESCE(discovered_from, '') FROM abom_frontier WHERE node_key = ?`,
		"acme/service").Scan(&from); err != nil {
		t.Fatalf("reading discovered_from: %v", err)
	}
	if from != "" {
		t.Errorf("discovered_from = %q, want nothing claimed", from)
	}
}

// queued reads the whole queue, whatever its state.
func (h *seedHarness) queued() []queuedRow {
	h.t.Helper()
	rows, err := h.store.DB().Query(
		`SELECT node_key, COALESCE(owner,''), COALESCE(repo,''), depth
		   FROM abom_frontier ORDER BY node_key`)
	if err != nil {
		h.t.Fatalf("reading the queue: %v", err)
	}
	defer rows.Close()

	var out []queuedRow
	for rows.Next() {
		var r queuedRow
		if err := rows.Scan(&r.NodeKey, &r.Owner, &r.Repo, &r.Depth); err != nil {
			h.t.Fatalf("scanning a queue row: %v", err)
		}
		out = append(out, r)
	}
	return out
}

type queuedRow struct {
	NodeKey string
	Owner   string
	Repo    string
	Depth   int
}
