package store

import "testing"

// declaringApp is the subgraph fixture with only octo-org/app declared.
func declaringApp(t *testing.T) *Store {
	t.Helper()
	s := subgraphFixture(t)
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "octo-org", Kind: AccountOrganization,
		Enabled: true, IncludesOnly: true, Includes: []string{"app"}}}, DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTheOwnedScopeKeepsWhatTheDeclarationReaches(t *testing.T) {
	g, err := declaringApp(t).FullGraph(FullGraphOptions{Scope: ScopeOwned})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := keysOf(g)
	for _, want := range []string{
		"octo-org/app/.github/workflows/ci.yml",
		// In another repository, and in scope because app runs it.
		"octo-org/shared/.github/workflows/build.yml",
	} {
		if !keys[want] {
			t.Errorf("%s is inside the declared closure and was dropped", want)
		}
	}
}

func TestTheOwnedScopeDropsWhatTheDeclarationDoesNotReach(t *testing.T) {
	g, err := declaringApp(t).FullGraph(FullGraphOptions{Scope: ScopeOwned})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := keysOf(g)
	for _, gone := range []string{
		"octo-org/tool/.github/workflows/ci.yml",
		// The repository app depends on holds its own CI, which app does not run.
		"octo-org/shared/.github/workflows/ci.yml",
	} {
		if keys[gone] {
			t.Errorf("%s is outside the declared closure and was kept", gone)
		}
	}
}

func TestTheOwnedScopeKeepsOnlyEdgesBetweenNodesItKept(t *testing.T) {
	g, err := declaringApp(t).FullGraph(FullGraphOptions{Scope: ScopeOwned})
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
