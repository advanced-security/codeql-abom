package expand

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// seedHarness is a lighter-weight fixture than harness: SeedFrontier only reads the store and
// writes the queue, so it needs neither an Expander nor any of the CodeQL/git fakes.
type seedHarness struct {
	t     *testing.T
	store *store.Store
	queue *frontier.Queue
}

// parseExternalKey splits an external node key into the owner, repo and path components a real
// query result would have carried, so the fixtures here resemble genuine ingested data.
func parseExternalKey(key string) (owner, repo, path string) {
	parts := strings.SplitN(key, "/", 3)
	if len(parts) > 0 {
		owner = parts[0]
	}
	if len(parts) > 1 {
		repo = parts[1]
	}
	if len(parts) > 2 {
		path = parts[2]
	}
	return owner, repo, path
}

func newSeedHarness(t *testing.T) *seedHarness {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return &seedHarness{t: t, store: s, queue: &frontier.Queue{Store: s, MaxDepth: 3}}
}

// declare adds a repository's account to the declared scope, additively.
func (h *seedHarness) declare(repoFullName string) {
	h.t.Helper()
	owner, _, _ := strings.Cut(repoFullName, "/")
	declared, err := h.store.OwnedScope()
	if err != nil {
		h.t.Fatalf("OwnedScope: %v", err)
	}
	for _, a := range declared {
		if strings.EqualFold(a.Owner, owner) {
			return
		}
	}
	declared = append(declared, store.DeclaredAccount{
		Owner: owner, Kind: store.AccountOrganization, Enabled: true,
	})
	if err := h.store.SetOwnedScope(declared, store.DeclaredByCLI); err != nil {
		h.t.Fatalf("SetOwnedScope: %v", err)
	}
}

// reference records repoFullName using targetKey, as a real query's edges table would. origin
// distinguishes a repository under review from one only fetched to resolve dependencies.
func (h *seedHarness) reference(repoFullName, origin, targetKey string) {
	h.t.Helper()
	if origin == store.OriginSeed {
		h.declare(repoFullName)
	}
	var (
		id  int64
		err error
	)
	switch origin {
	case store.OriginUpstream:
		id, err = h.store.InsertUpstreamRepository(repoFullName)
	default:
		id, err = h.store.UpsertRepository(repoFullName, "")
	}
	if err != nil {
		h.t.Fatalf("inserting repository: %v", err)
	}

	owner, repo, path := parseExternalKey(targetKey)
	w := store.NewWriter(h.store, 0)
	workflow := repoFullName + "/.github/workflows/ci.yml"
	step := repoFullName + "-step"
	for _, n := range []store.Node{
		{Key: targetKey, Kind: "workflow", Owner: owner, Repo: repo, Path: path, IsExternal: true},
		{Key: workflow, Kind: "workflow", RepositoryID: id},
		{Key: step, Kind: "step", RepositoryID: id, ContainerKey: workflow},
	} {
		if err := w.AddNode(n); err != nil {
			h.t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(store.Edge{
		Key: repoFullName + "-" + targetKey + "-uses", SrcKey: step, DstKey: targetKey,
		Kind: "job-uses-reusable-workflow", FilePath: ".github/workflows/ci.yml",
		StartLine: 7, RepositoryID: id,
	}); err != nil {
		h.t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		h.t.Fatalf("Flush: %v", err)
	}
}

// A repository under review is exactly the case the frontier must start from: nobody has
// analyzed its dependencies yet, so there is no prior processing to key a seed off of.
func TestSeedFrontierQueuesASeedRepositoriesDependencies(t *testing.T) {
	h := newSeedHarness(t)
	h.reference("org/app", store.OriginSeed, "actions/checkout")

	n, err := SeedFrontier(h.store, h.queue)
	if err != nil {
		t.Fatalf("SeedFrontier: %v", err)
	}
	if n != 1 {
		t.Errorf("SeedFrontier returned %d, want 1", n)
	}

	depth, ok := queuedDepth(t, h.store, "actions/checkout")
	if !ok {
		t.Fatal("actions/checkout was not queued")
	}
	if depth != 1 {
		t.Errorf("depth = %d, want 1: the seed repository names it directly", depth)
	}
}

// "Why is this queued at all" has exactly one answer, and it stops being recoverable once the
// dependency is analyzed: the edge that put it there is then indistinguishable from any other.
func TestSeedingRecordsWhichRepositoryNamedTheDependency(t *testing.T) {
	h := newSeedHarness(t)
	h.reference("org/app", store.OriginSeed, "actions/checkout")

	if _, err := SeedFrontier(h.store, h.queue); err != nil {
		t.Fatalf("SeedFrontier: %v", err)
	}

	var from string
	if err := h.store.DB().QueryRow(
		`SELECT COALESCE(discovered_from,'') FROM abom_frontier WHERE node_key='actions/checkout'`).
		Scan(&from); err != nil {
		t.Fatalf("reading provenance: %v", err)
	}
	if from != "org/app" {
		t.Errorf("discovered_from = %q, want the repository that names it", from)
	}
}

// Re-seeding never resets an entry, so a row queued before provenance was recorded would keep an
// empty answer for good unless filling in a missing one is allowed.
func TestSeedingFillsProvenanceThatWasMissing(t *testing.T) {
	h := newSeedHarness(t)
	h.reference("org/app", store.OriginSeed, "actions/checkout")
	if _, err := h.store.DB().Exec(`
		INSERT INTO abom_frontier (node_key, owner, repo, depth, status, enqueued_at)
		VALUES ('actions/checkout', 'actions', 'checkout', 1, 'pending', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seeding a row without provenance: %v", err)
	}

	if _, err := SeedFrontier(h.store, h.queue); err != nil {
		t.Fatalf("SeedFrontier: %v", err)
	}

	var from string
	if err := h.store.DB().QueryRow(
		`SELECT COALESCE(discovered_from,'') FROM abom_frontier WHERE node_key='actions/checkout'`).
		Scan(&from); err != nil {
		t.Fatalf("reading provenance: %v", err)
	}
	if from != "org/app" {
		t.Errorf("discovered_from = %q, want it backfilled", from)
	}
}

// An upstream repository was fetched only to resolve a seed's dependencies; its own edges are// not a supply chain anyone under review actually has, so seeding must not start from them.
func TestSeedFrontierIgnoresUpstreamRepositories(t *testing.T) {
	h := newSeedHarness(t)
	h.reference("octo/upstream-only", store.OriginUpstream, "actions/never-queued")

	n, err := SeedFrontier(h.store, h.queue)
	if err != nil {
		t.Fatalf("SeedFrontier: %v", err)
	}
	if n != 0 {
		t.Errorf("SeedFrontier returned %d, want 0", n)
	}
	if _, ok := queuedDepth(t, h.store, "actions/never-queued"); ok {
		t.Error("queued a dependency reachable only through an upstream repository")
	}
}

// Internal nodes (a seed repository's own workflow files and steps) are not external
// dependencies and must never enter a queue whose purpose is following the supply chain out.
func TestSeedFrontierIgnoresInternalNodes(t *testing.T) {
	h := newSeedHarness(t)
	h.reference("org/app", store.OriginSeed, "actions/checkout")

	if _, err := SeedFrontier(h.store, h.queue); err != nil {
		t.Fatalf("SeedFrontier: %v", err)
	}
	if _, ok := queuedDepth(t, h.store, "org/app/.github/workflows/ci.yml"); ok {
		t.Error("queued a seed repository's own internal workflow node")
	}
}

// Running discover and build again before expand-all must not re-litigate already-claimed or
// completed frontier entries; Enqueue's own MIN(depth) upsert already guarantees this, and
// SeedFrontier must not bypass it with, say, a raw INSERT.
func TestSeedFrontierIsSafeToRunTwice(t *testing.T) {
	h := newSeedHarness(t)
	h.reference("org/app", store.OriginSeed, "actions/checkout")

	if _, err := SeedFrontier(h.store, h.queue); err != nil {
		t.Fatalf("first SeedFrontier: %v", err)
	}
	if _, err := h.queue.Claim(10); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	n, err := SeedFrontier(h.store, h.queue)
	if err != nil {
		t.Fatalf("second SeedFrontier: %v", err)
	}
	if n != 0 {
		t.Errorf("second SeedFrontier returned %d, want 0 new items", n)
	}
}
