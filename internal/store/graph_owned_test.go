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

// What you own is what the declared repository holds; a workflow it runs from another repository is
// that repository's.
func TestTheOwnedScopeKeepsOnlyWhatTheDeclarationHolds(t *testing.T) {
	g, err := declaringApp(t).FullGraph(FullGraphOptions{Scope: ScopeOwned})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	keys := keysOf(g)
	if !keys["octo-org/app/.github/workflows/ci.yml"] {
		t.Error("the declared repository's own workflow was dropped")
	}
	for _, gone := range []string{
		// app runs it, and octo-org/shared holds it.
		"octo-org/shared/.github/workflows/build.yml",
		"actions/checkout",
		"octo-org/tool/.github/workflows/ci.yml",
	} {
		if keys[gone] {
			t.Errorf("%s is not held by a declared repository and was kept", gone)
		}
	}
}

func TestTheSupplyChainScopeKeepsWhatTheDeclarationReaches(t *testing.T) {
	g, err := declaringApp(t).FullGraph(FullGraphOptions{Scope: ScopeSupplyChain})
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

func TestTheSupplyChainScopeDropsWhatTheDeclarationDoesNotReach(t *testing.T) {
	g, err := declaringApp(t).FullGraph(FullGraphOptions{Scope: ScopeSupplyChain})
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

func TestEitherDeclaredScopeKeepsOnlyEdgesBetweenNodesItKept(t *testing.T) {
	for _, scope := range []string{ScopeOwned, ScopeSupplyChain} {
		g, err := declaringApp(t).FullGraph(FullGraphOptions{Scope: scope})
		if err != nil {
			t.Fatalf("FullGraph(%s): %v", scope, err)
		}

		keys := keysOf(g)
		for _, e := range g.Edges {
			if !keys[e.Source] || !keys[e.Target] {
				t.Errorf("%s: edge %s -> %s has an end outside the scope", scope, e.Source, e.Target)
			}
		}
	}
}

// Declaring nothing must show nothing; an empty declaration is not the whole corpus.
func TestEitherDeclaredScopeShowsNothingWhenNothingIsDeclared(t *testing.T) {
	for _, scope := range []string{ScopeOwned, ScopeSupplyChain} {
		g, err := subgraphFixture(t).FullGraph(FullGraphOptions{Scope: scope})
		if err != nil {
			t.Fatalf("FullGraph(%s): %v", scope, err)
		}

		if len(g.Nodes) != 0 || len(g.Edges) != 0 {
			t.Errorf("%s: an undeclared scope drew %d nodes and %d edges; it must draw none",
				scope, len(g.Nodes), len(g.Edges))
		}
	}
}

// The workflows unit asks the same question: the reusable workflow app calls is run by what you
// own and held by octo-org/shared, so it is in the supply chain and not yours.
func TestTheWorkflowsUnitListsOnlyWhatYouHoldUnderWhatYouOwn(t *testing.T) {
	s := declaringApp(t)
	for scope, calledListed := range map[string]bool{ScopeOwned: false, ScopeSupplyChain: true} {
		page, err := s.WorkflowsPage(WorkflowsQuery{Scope: scope})
		if err != nil {
			t.Fatalf("WorkflowsPage(%s): %v", scope, err)
		}
		keys := map[string]bool{}
		for _, w := range page.Items {
			keys[w.NodeKey] = true
		}
		if !keys["octo-org/app/.github/workflows/ci.yml"] {
			t.Errorf("%s: the declared repository's own workflow is missing", scope)
		}
		if got := keys["octo-org/shared/.github/workflows/build.yml"]; got != calledListed {
			t.Errorf("%s: the reusable workflow app calls is listed=%v, want %v", scope, got, calledListed)
		}
	}
}
