package store

import "testing"

// Owned scope must match the closure the crawl is bounded by and pruning acts on.
func TestTheOwnedScopeKeepsWhatTheDeclarationReaches(t *testing.T) {
	g, err := subgraphFixture(t).FullGraph(FullGraphOptions{
		Scope: ScopeOwned, Closure: []string{"octo-org/app"},
	})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := keysOf(g)
	for _, want := range []string{
		"octo-org/app/.github/workflows/ci.yml",
		// Reached only through app's edge, so this is the clause that makes a closure a closure
		// rather than a list of repositories.
		"octo-org/shared/.github/workflows/build.yml",
	} {
		if !keys[want] {
			t.Errorf("%s is inside the declared closure and was dropped", want)
		}
	}
}

func TestTheOwnedScopeDropsWhatTheDeclarationDoesNotReach(t *testing.T) {
	g, err := subgraphFixture(t).FullGraph(FullGraphOptions{
		Scope: ScopeOwned, Closure: []string{"octo-org/app"},
	})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := keysOf(g)
	for _, gone := range []string{
		"octo-org/tool/.github/workflows/ci.yml",
		"octo-org/shared/.github/workflows/ci.yml",
		// Used by tool and by shared's own workflows, never on anything app reaches.
		"actions/checkout",
	} {
		if keys[gone] {
			t.Errorf("%s is outside the declared closure and was kept", gone)
		}
	}
}

func TestTheOwnedScopeKeepsOnlyEdgesBetweenNodesItKept(t *testing.T) {
	g, err := subgraphFixture(t).FullGraph(FullGraphOptions{
		Scope: ScopeOwned, Closure: []string{"octo-org/app"},
	})
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

// Declaring nothing must show nothing; an empty closure is not the whole corpus.
func TestTheOwnedScopeShowsNothingWhenNothingIsDeclared(t *testing.T) {
	g, err := subgraphFixture(t).FullGraph(FullGraphOptions{Scope: ScopeOwned})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	if len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Errorf("an undeclared scope drew %d nodes and %d edges; it must draw none",
			len(g.Nodes), len(g.Edges))
	}
}
