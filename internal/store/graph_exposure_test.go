package store

import (
	"encoding/json"
	"testing"
)

// exposeFixture records that octo-org/app reaches actions/checkout through octo-org/shared's
// reusable workflow, which is the shape BuildExposure writes for an advisory-affected action.
func exposeFixture(t *testing.T) *Store {
	t.Helper()
	s := subgraphFixture(t)

	var repoID, nodeID int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = 'octo-org/app'`).
		Scan(&repoID); err != nil {
		t.Fatalf("reading repository: %v", err)
	}
	if err := s.DB().QueryRow(
		`SELECT row_id FROM abom_node WHERE node_key = 'actions/checkout'`).Scan(&nodeID); err != nil {
		t.Fatalf("reading node: %v", err)
	}
	path, err := json.Marshal([]string{
		"octo-org/app/.github/workflows/ci.yml",
		"octo-org/shared/.github/workflows/build.yml",
		"actions/checkout",
	})
	if err != nil {
		t.Fatalf("encoding path: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_exposure (repository_row_id, node_row_id, ghsa_id, hops, path)
		VALUES (?, ?, 'GHSA-test-0000-0000', 2, ?)`, repoID, nodeID, string(path)); err != nil {
		t.Fatalf("recording exposure: %v", err)
	}
	return s
}

func keysOf(g Graph) map[string]bool {
	out := map[string]bool{}
	for _, n := range g.Nodes {
		out[n.Key] = true
	}
	return out
}

// Exposure scope is exactly the routes that reach an advisory-affected action.
func TestTheExposureScopeKeepsEveryNodeOnARoute(t *testing.T) {
	g, err := exposeFixture(t).FullGraph(FullGraphOptions{Scope: ScopeExposure})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := keysOf(g)
	for _, want := range []string{
		"octo-org/app/.github/workflows/ci.yml",
		"octo-org/shared/.github/workflows/build.yml",
		"actions/checkout",
	} {
		if !keys[want] {
			t.Errorf("%s is on the route and was dropped", want)
		}
	}
}

// The route is the object, not the affected action: a reader needs to know whether the fix is
// theirs or an intermediary's, and only the intervening nodes say which.
func TestTheExposureScopeDropsWhatIsOnNoRoute(t *testing.T) {
	g, err := exposeFixture(t).FullGraph(FullGraphOptions{Scope: ScopeExposure})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	if keysOf(g)["octo-org/tool/.github/workflows/ci.yml"] {
		t.Error("octo-org/tool reaches nothing an advisory names, so it is not in this scope")
	}
}

// An edge with one end outside the scope would draw into empty space.
func TestTheExposureScopeKeepsOnlyEdgesBetweenNodesItKept(t *testing.T) {
	g, err := exposeFixture(t).FullGraph(FullGraphOptions{Scope: ScopeExposure})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := keysOf(g)
	for _, e := range g.Edges {
		if !keys[e.Source] || !keys[e.Target] {
			t.Errorf("edge %s -> %s has an end outside the scope", e.Source, e.Target)
		}
	}
}

// Nothing exposed and nothing analyzed must not look alike, so the scope answers empty rather
// than falling back to the whole graph.
func TestAnUnexposedCorpusScopesToNothingRatherThanEverything(t *testing.T) {
	g, err := subgraphFixture(t).FullGraph(FullGraphOptions{Scope: ScopeExposure})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	if len(g.Nodes) != 0 {
		t.Errorf("got %d nodes, want none: no advisory reaches this corpus", len(g.Nodes))
	}
}

// The unscoped view is still the honest answer to "what do we depend on", so asking for no scope
// has to keep returning everything.
func TestNoScopeStillReturnsTheWholeGraph(t *testing.T) {
	g, err := exposeFixture(t).FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	if !keysOf(g)["octo-org/tool/.github/workflows/ci.yml"] {
		t.Error("the unscoped graph lost a node that is simply not exposed")
	}
}
