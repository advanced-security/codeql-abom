package store

import "testing"

func TestRepositoriesFindFindingsByRepositoryAndOrderByWorstSeverity(t *testing.T) {
	s := advisoryStore(t)
	for _, name := range []string{"octo-org/critical", "octo-org/many", "octo-org/clean"} {
		seedDependency(t, s, name, "actions/checkout", "v4", 1)
	}
	critical := 9.8
	medium := 5.0
	for name, scores := range map[string][]*float64{
		"octo-org/critical": {&critical},
		"octo-org/many":     {&medium, &medium},
	} {
		repositoryID, err := s.UpsertRepository(name, "")
		if err != nil {
			t.Fatal(err)
		}
		for index, score := range scores {
			if _, err := s.AddFindings([]Finding{{RepositoryID: repositoryID, RuleID: "actions/example",
				FilePath: "workflow.yml", StartLine: index + 1, SecuritySeverity: score}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.ReplacePropertyValues("octo-org/critical", []PropertyValue{
		{Name: "team", Value: "payments"}, {Name: "tier", Value: "production"},
	}); err != nil {
		t.Fatal(err)
	}

	page, err := s.RepositoriesPage(RepositoriesQuery{Flags: []string{"findings"}, Sort: "findings", Desc: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Items[0].FullName != "octo-org/critical" || page.Items[1].FullName != "octo-org/many" {
		t.Fatalf("repositories with findings were not ordered worst first: %+v", page.Items)
	}
	if got := page.Items[0].Properties; len(got) != 2 || got[0].Name != "team" || got[1].Value != "production" {
		t.Fatalf("repository properties were not returned: %+v", got)
	}
	if page.Items[1].Properties == nil {
		t.Fatal("a repository without properties must return an empty list")
	}
}

// The repositories unit refused `scope`, and the built-in tab that names it — "What you own" —
// therefore answered a page about the repositories you own with a refusal and an empty grid, under
// a heading saying "The repositories you own". A term this unit plainly answers has to be applied,
// not declined.
func TestRepositoriesScopeOwnedKeepsOnlyTheDeclaration(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/a", "actions/checkout", "v4", 1)
	seedDependency(t, s, "other-org/b", "actions/checkout", "v4", 1)
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "octo-org", Kind: AccountOrganization,
		Enabled: true}}, DeclaredByCLI); err != nil {
		t.Fatal(err)
	}

	page, err := s.RepositoriesPage(RepositoriesQuery{Scope: ScopeOwned})
	if err != nil {
		t.Fatalf("RepositoriesPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("matched %d repositories, want the 1 declared", page.Total)
	}
	if page.Items[0].FullName != "octo-org/a" {
		t.Errorf("listed %q, want the declared repository", page.Items[0].FullName)
	}
}

// Declaring nothing shows nothing, for the reason the node listing does: falling through to every
// repository answers a question about the operator's own estate with somebody else's.
func TestRepositoriesOwnedScopeWithNoDeclarationShowsNothing(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/a", "actions/checkout", "v4", 1)

	page, err := s.RepositoriesPage(RepositoriesQuery{Scope: ScopeOwned})
	if err != nil {
		t.Fatalf("RepositoriesPage: %v", err)
	}
	if page.Total != 0 {
		t.Errorf("matched %d repositories with nothing declared, want none", page.Total)
	}
}

// `scope=exposure` asks which repositories sit on an advisory route. A repository with no exposure
// row is not on one, and counting it would report a route nothing measured.
func TestRepositoriesExposureScopeKeepsOnlyRepositoriesOnARoute(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/exposed", "actions/checkout", "v4", 1)
	seedDependency(t, s, "octo-org/clear", "actions/cache", "v4", 1)
	exposeRepo(t, s, "octo-org/exposed", "GHSA-xxxx-yyyy-zzzz", "actions/checkout", 1)

	page, err := s.RepositoriesPage(RepositoriesQuery{Scope: ScopeExposure})
	if err != nil {
		t.Fatalf("RepositoriesPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("matched %d repositories, want the 1 on a route", page.Total)
	}
	if page.Items[0].FullName != "octo-org/exposed" {
		t.Errorf("listed %q, want the repository on an advisory route", page.Items[0].FullName)
	}
}
