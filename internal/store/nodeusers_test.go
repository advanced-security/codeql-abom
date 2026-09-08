package store

import (
	"testing"
)

// The Discovery grid previews five names and says "and 280 more", which is honest and a dead end
// — 291 of the corpus's 5,787 name-and-version pairs end there. This is the read behind the rest
// of the list.
func TestEveryRepositoryNamingAVersionIsListed(t *testing.T) {
	s := subgraphFixture(t)

	users, err := s.NodeUsersOf("actions/checkout", "v4")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}

	got := map[string]bool{}
	for _, r := range users.Repos {
		got[r.FullName] = true
	}
	for _, want := range []string{"octo-org/tool", "octo-org/shared"} {
		if !got[want] {
			t.Errorf("%s names actions/checkout@v4 and is missing from %v", want, got)
		}
	}
	if users.Total != len(users.Repos) {
		t.Errorf("Total = %d but %d repositories were listed", users.Total, len(users.Repos))
	}
}

// A version is the unit, not the package. Listing every user of the node would answer a question
// nobody asked from a row that names one reference.
func TestTheListIsScopedToOneReferenceNotTheWholeNode(t *testing.T) {
	s := subgraphFixture(t)

	users, err := s.NodeUsersOf("actions/checkout", "v3")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}

	if len(users.Repos) != 1 || users.Repos[0].FullName != "octo-org/tool" {
		t.Fatalf("v3 is used only by octo-org/tool, got %v", users.Repos)
	}
}

// Upstream repositories contribute edges but are not dependents: they were fetched only to
// resolve what the repositories in scope rely on. The list carries the distinction rather than
// dropping them, because "who runs this version" is a different question from "who would I ask
// to change it" — but a reader must be able to tell which is which.
func TestAnUpstreamRepositoryIsListedAndSaysSo(t *testing.T) {
	s := subgraphFixture(t)

	users, err := s.NodeUsersOf("actions/checkout", "v4")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}

	for _, r := range users.Repos {
		if r.FullName == "octo-org/shared" && r.Origin != OriginUpstream {
			t.Errorf("octo-org/shared origin = %q, want %q", r.Origin, OriginUpstream)
		}
		if r.FullName == "octo-org/tool" && r.Origin != OriginSeed {
			t.Errorf("octo-org/tool origin = %q, want %q", r.Origin, OriginSeed)
		}
	}
	if users.Seeds != 1 {
		t.Errorf("Seeds = %d, want 1: only octo-org/tool is in scope", users.Seeds)
	}
}

// octo-org/shared names v4 twice, from two different workflows. That is one repository with two
// places to edit, and collapsing it to one hides half the work while counting it twice hides the
// repository.
func TestARepositoryNamingAVersionTwiceIsOneRowWithTwoUses(t *testing.T) {
	s := subgraphFixture(t)

	users, err := s.NodeUsersOf("actions/checkout", "v4")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}

	seen := 0
	for _, r := range users.Repos {
		if r.FullName != "octo-org/shared" {
			continue
		}
		seen++
		if r.Uses != 2 {
			t.Errorf("octo-org/shared uses = %d, want 2", r.Uses)
		}
	}
	if seen != 1 {
		t.Errorf("octo-org/shared appears %d times, want once", seen)
	}
}

// With no verified account, "0 yours" is an unanswered question rather than a count of none —
// the same rule the pinning split and the Discovery label already follow.
func TestOwnershipIsUnansweredUntilAnAccountIsVerified(t *testing.T) {
	s := subgraphFixture(t)

	users, err := s.NodeUsersOf("actions/checkout", "v4")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}
	if users.OwnedKnown {
		t.Error("no account is verified, so ownership cannot be reported as known")
	}
	for _, r := range users.Repos {
		if r.Owned {
			t.Errorf("%s is marked owned with nothing verified", r.FullName)
		}
	}
}

func TestADeclaredAccountMarksItsOwnRepositories(t *testing.T) {
	s := subgraphFixture(t)
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo-org", Kind: AccountOrganization, Enabled: true},
	}); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	users, err := s.NodeUsersOf("actions/checkout", "v4")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}
	if !users.OwnedKnown {
		t.Fatal("a scope is declared, so ownership is answerable")
	}
	if users.Owned != users.Total {
		t.Errorf("Owned = %d of %d; every repository here is under octo-org",
			users.Owned, users.Total)
	}
}

// An empty reference is *default branch*, a real answer that 1,253 rows give. Treating it as a
// wildcard would make one row list every user of the action.
func TestAnEmptyReferenceIsTheDefaultBranchNotEveryReference(t *testing.T) {
	s := subgraphFixture(t)

	users, err := s.NodeUsersOf("actions/checkout", "")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}
	if len(users.Repos) != 0 {
		t.Errorf("nothing names actions/checkout without a reference, got %v", users.Repos)
	}
}

func TestAnUnknownNodeYieldsAnEmptyListRatherThanNil(t *testing.T) {
	s := subgraphFixture(t)

	users, err := s.NodeUsersOf("nobody/nothing", "v1")
	if err != nil {
		t.Fatalf("NodeUsersOf: %v", err)
	}
	if users.Repos == nil {
		t.Fatal("nil marshals to JSON null and breaks the C# client")
	}
	if users.Total != 0 || users.Truncated {
		t.Errorf("Total = %d, Truncated = %v; want an empty, complete answer",
			users.Total, users.Truncated)
	}
}

// A budget that bites without saying so is a list that looks complete and is not. Total counts
// the whole set; the rows are what fitted.
func TestABudgetedListSaysItWasTruncated(t *testing.T) {
	s := subgraphFixture(t)

	users, err := s.nodeUsersOf("actions/checkout", "v4", 1)
	if err != nil {
		t.Fatalf("nodeUsersOf: %v", err)
	}
	if len(users.Repos) != 1 {
		t.Fatalf("want the budget honored, got %d rows", len(users.Repos))
	}
	if !users.Truncated {
		t.Error("the list was cut and does not say so")
	}
	if users.Total != 2 {
		t.Errorf("Total = %d, want the full count of 2 regardless of the budget", users.Total)
	}
}
