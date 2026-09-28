package expand

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Builds a seed workflow pinning one composite action, alongside two rows that must not be
// touched: a ref of the same action that nothing pins, and a dependency with no steps.
func compositePinFixture(t *testing.T, s *store.Store) {
	t.Helper()

	var seed int64
	if err := s.DB().QueryRow(
		`INSERT INTO repository (repository_full_name, origin) VALUES ('acme/app','seed')
		 RETURNING row_id`).Scan(&seed); err != nil {
		t.Fatalf("seeding the repository: %v", err)
	}
	node := func(key, owner, repo string) int64 {
		t.Helper()
		var id int64
		if err := s.DB().QueryRow(`
			INSERT INTO abom_node (node_key, node_kind, owner, repo, is_external)
			VALUES (?, 'action', ?, ?, 1) RETURNING row_id`, key, owner, repo).
			Scan(&id); err != nil {
			t.Fatalf("inserting %s: %v", key, err)
		}
		return id
	}
	edge := func(key string, dst int64, ref string) {
		t.Helper()
		if _, err := s.DB().Exec(`
			INSERT INTO abom_edge (edge_key, src_node_row_id, dst_node_row_id, edge_kind,
			                       ref_value, file_path, repository_row_id, start_line)
			VALUES (?, ?, ?, 'step-uses-action', ?, '.github/workflows/ci.yml', ?, 1)`,
			key, dst, dst, ref, seed); err != nil {
			t.Fatalf("inserting edge %s: %v", key, err)
		}
	}

	composite := node("vendor/composite", "vendor", "composite")
	plain := node("vendor/plain", "vendor", "plain")

	// Only the composite one contains steps, so only it can have children.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_node (node_key, node_kind, is_external, container_node_row_id)
		VALUES ('step:vendor/composite:action.yml::0','step',0,?)`, composite); err != nil {
		t.Fatalf("inserting the composite step: %v", err)
	}

	edge("p1", composite, "v1")
	edge("p2", composite, "v2")
	edge("p3", plain, "v9")

	// The queue already holds all of these: SeedFrontier groups by (node_key, ref) and enqueues
	// each at depth 1. v3 is a ref the crawl found elsewhere that no workflow pins.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('vendor/composite','v1','vendor','composite',1,'done','2026-01-01T00:00:00Z'),
		       ('vendor/composite','v2','vendor','composite',1,'pending','2026-01-01T00:00:00Z'),
		       ('vendor/composite','v3','vendor','composite',2,'pending','2026-01-01T00:00:00Z'),
		       ('vendor/plain','v9','vendor','plain',1,'pending','2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}
}

func expeditedAt(t *testing.T, s *store.Store, key, ref string) string {
	t.Helper()
	var at string
	if err := s.DB().QueryRow(
		`SELECT COALESCE(expedited_at,'') FROM abom_frontier WHERE node_key=? AND ref=?`,
		key, ref).Scan(&at); err != nil {
		t.Fatalf("reading expedited_at for %s@%s: %v", key, ref, err)
	}
	return at
}

// The reference is the whole point. Expediting by node key alone would prioritize every version of
// an action the crawl has ever seen, spending a checkout and a database build on trees no consumer
// pins — the same waste this workstream exists to avoid.
func TestOnlyThePinnedRefIsExpedited(t *testing.T) {
	h := newSeedHarness(t)
	s, q := h.store, h.queue
	compositePinFixture(t, s)

	if _, err := ExpediteCompositePins(s, q); err != nil {
		t.Fatalf("ExpediteCompositePins: %v", err)
	}

	if expeditedAt(t, s, "vendor/composite", "v2") == "" {
		t.Error("the pinned ref was not expedited")
	}
	if at := expeditedAt(t, s, "vendor/composite", "v3"); at != "" {
		t.Errorf("a ref nobody pins was expedited (%s)", at)
	}
}

// Nothing is enqueued here; SeedFrontier already queues every (node, ref) an owned workflow names.
func TestExpeditingAddsNoQueueRows(t *testing.T) {
	h := newSeedHarness(t)
	s, q := h.store, h.queue
	compositePinFixture(t, s)

	var before int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_frontier`).Scan(&before); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if _, err := ExpediteCompositePins(s, q); err != nil {
		t.Fatalf("ExpediteCompositePins: %v", err)
	}
	var after int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_frontier`).Scan(&after); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if before != after {
		t.Errorf("queue went from %d to %d rows; this reprioritizes, it does not enqueue",
			before, after)
	}
}

// A dependency with no steps cannot have children, so prioritizing it spends the crawl on work
// that cannot produce a transitive edge.
func TestANonCompositeDependencyIsNotExpedited(t *testing.T) {
	h := newSeedHarness(t)
	s, q := h.store, h.queue
	compositePinFixture(t, s)

	if _, err := ExpediteCompositePins(s, q); err != nil {
		t.Fatalf("ExpediteCompositePins: %v", err)
	}
	if at := expeditedAt(t, s, "vendor/plain", "v9"); at != "" {
		t.Errorf("a dependency with no steps was expedited (%s)", at)
	}
}

// Re-analyzing a ref already covered spends a full checkout to learn nothing, and would stop the
// coverage gap ever reaching zero.
func TestAnAnalyzedRefIsNotExpedited(t *testing.T) {
	h := newSeedHarness(t)
	s, q := h.store, h.queue
	compositePinFixture(t, s)

	if _, err := ExpediteCompositePins(s, q); err != nil {
		t.Fatalf("ExpediteCompositePins: %v", err)
	}
	if at := expeditedAt(t, s, "vendor/composite", "v1"); at != "" {
		t.Errorf("an already-analyzed ref was expedited (%s)", at)
	}
}

// A settled corpus expedites nothing, and must not report work it did not do.
func TestAFullyAnalyzedCorpusExpeditesNothing(t *testing.T) {
	h := newSeedHarness(t)
	s, q := h.store, h.queue

	n, err := ExpediteCompositePins(s, q)
	if err != nil {
		t.Fatalf("ExpediteCompositePins: %v", err)
	}
	if n != 0 {
		t.Errorf("expedited %d items from an empty corpus", n)
	}
}
