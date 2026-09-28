package store

import "testing"

// Bump impact is the answer to "who breaks if I change this", so the split between what a
// repository can fix itself and what it can only wait on has to survive.
func TestBumpImpactSeparatesDirectFromTransitive(t *testing.T) {
	s := subgraphFixture(t)

	impact, err := s.BumpImpactOf("actions/checkout")
	if err != nil {
		t.Fatalf("BumpImpactOf: %v", err)
	}

	byName := map[string]AffectedRepo{}
	for _, r := range impact.Repos {
		byName[r.FullName] = r
	}

	// tool names checkout in its own workflow; app reaches it only through the upstream
	// reusable workflow it calls.
	if got := byName["octo-org/tool"].Hops; got != 1 {
		t.Errorf("octo-org/tool hops = %d, want 1", got)
	}
	if got := byName["octo-org/app"].Hops; got < 2 {
		t.Errorf("octo-org/app hops = %d, want more than 1", got)
	}
	if impact.Direct < 1 || impact.Transitive < 1 {
		t.Errorf("direct = %d, transitive = %d; both should be populated",
			impact.Direct, impact.Transitive)
	}
}

// Upstream repositories are resolution context, not repositories the operator can change.
func TestBumpImpactExcludesUpstreamRepositories(t *testing.T) {
	s := subgraphFixture(t)

	impact, err := s.BumpImpactOf("actions/checkout")
	if err != nil {
		t.Fatalf("BumpImpactOf: %v", err)
	}
	for _, r := range impact.Repos {
		if r.Origin == OriginUpstream {
			t.Errorf("upstream repository %s counted as affected", r.FullName)
		}
	}
}

// A repository that names the action carries the reference it would have to change. One that
// reaches it through an intermediary has none of its own, and must not be given a borrowed one.
func TestBumpImpactCarriesEachRepositoryOwnReferences(t *testing.T) {
	s := subgraphFixture(t)

	impact, err := s.BumpImpactOf("actions/checkout")
	if err != nil {
		t.Fatalf("BumpImpactOf: %v", err)
	}

	for _, r := range impact.Repos {
		if r.Refs == nil {
			t.Fatalf("%s has nil refs; an empty list is the contract", r.FullName)
		}
		if r.FullName == "octo-org/tool" && len(r.Refs) != 2 {
			t.Errorf("octo-org/tool refs = %v, want both v3 and v4", r.Refs)
		}
	}
}

// Anything reachable only through a dependency that was never expanded cannot appear at all, so
// the count of what is still pending travels with the answer.
func TestBumpImpactReportsUnanalyzedDependencies(t *testing.T) {
	s := subgraphFixture(t)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, depth, status, score, enqueued_at)
		VALUES ('some/unexpanded', 1, 'pending', 1.0, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seeding frontier: %v", err)
	}

	impact, err := s.BumpImpactOf("actions/checkout")
	if err != nil {
		t.Fatalf("BumpImpactOf: %v", err)
	}
	if impact.Unanalyzed != 1 {
		t.Errorf("Unanalyzed = %d, want 1", impact.Unanalyzed)
	}
}

func TestBumpImpactOfUnknownNodeIsEmptyNotNil(t *testing.T) {
	s := subgraphFixture(t)

	impact, err := s.BumpImpactOf("nobody/nothing")
	if err != nil {
		t.Fatalf("BumpImpactOf: %v", err)
	}
	if impact.Repos == nil {
		t.Error("Repos is nil")
	}
	if impact.Direct != 0 || impact.Transitive != 0 {
		t.Errorf("unknown node reported %d/%d affected", impact.Direct, impact.Transitive)
	}
}
