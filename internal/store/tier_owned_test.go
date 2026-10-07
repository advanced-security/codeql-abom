package store

import "testing"

// The owned tier holds repositories and dependencies, but their analyzed counts answer different
// questions and must not be collapsed.
func TestTheOwnedTierSeparatesRepositoriesFromDependencies(t *testing.T) {
	s := newTestStore(t)
	declareOwner(t, s, "acme")
	enqueueRoot(t, s, "acme/service", "done")
	enqueueRoot(t, s, "acme/waiting", "pending")
	enqueueDependency(t, s, "acme/action", "v1", "pending")

	counts, err := s.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}
	owned := counts[TierOwned]

	if owned.Repositories != 2 || owned.RepositoriesAnalyzed != 1 {
		t.Errorf("repositories = %d of %d, want 1 of 2",
			owned.RepositoriesAnalyzed, owned.Repositories)
	}
	if owned.Dependencies != 1 || owned.DependenciesAnalyzed != 0 {
		t.Errorf("dependencies = %d of %d, want 0 of 1",
			owned.DependenciesAnalyzed, owned.Dependencies)
	}
}

// The split must reconcile to the tier, or a reader adding the two parts gets a third number that
// matches nothing else on the page.
func TestTheOwnedSplitReconcilesToTheTierTotal(t *testing.T) {
	s := newTestStore(t)
	declareOwner(t, s, "acme")
	enqueueRoot(t, s, "acme/service", "done")
	enqueueRoot(t, s, "acme/waiting", "pending")
	enqueueDependency(t, s, "acme/action", "v1", "done")
	enqueueDependency(t, s, "acme/other", "v2", "pending")

	counts, err := s.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}
	owned := counts[TierOwned]

	if owned.Repositories+owned.Dependencies != owned.Total {
		t.Errorf("%d repositories + %d dependencies != %d total",
			owned.Repositories, owned.Dependencies, owned.Total)
	}
	if owned.RepositoriesAnalyzed+owned.DependenciesAnalyzed != owned.Analyzed {
		t.Errorf("%d + %d analyzed != %d",
			owned.RepositoriesAnalyzed, owned.DependenciesAnalyzed, owned.Analyzed)
	}
}

// Only the owned tier can hold repositories: a root is the operator's own by construction, so a
// repository count on the other tiers would be a category error rather than a zero.
func TestOnlyTheOwnedTierHoldsRepositories(t *testing.T) {
	s := newTestStore(t)
	declareOwner(t, s, "acme")
	enqueueRoot(t, s, "acme/service", "done")
	enqueueDependency(t, s, "stranger/thing", "v1", "done")

	counts, err := s.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}

	for _, tier := range []string{TierDirect, TierTransitive} {
		if counts[tier].Repositories != 0 {
			t.Errorf("%s reports %d repositories", tier, counts[tier].Repositories)
		}
	}
}

func declareOwner(t *testing.T, s *Store, owner string) {
	t.Helper()
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: owner, Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
}

// enqueueRoot adds the depth-0 row that enumeration writes for a repository the operator owns.
func enqueueRoot(t *testing.T, s *Store, fullName, status string) {
	t.Helper()
	enqueueWithStatus(t, s, fullName, status)
	if _, err := s.db.Exec(
		`UPDATE abom_frontier SET status = ? WHERE node_key = ?`, status, fullName); err != nil {
		t.Fatalf("setting status of %q: %v", fullName, err)
	}
}

func enqueueDependency(t *testing.T, s *Store, nodeKey, ref, status string) {
	t.Helper()
	enqueueRef(t, s, nodeKey, ref, 1, status)
}

// A repository read in full with nothing to analyze is not analyzed, and it is not outstanding
// either: the tier states it apart, so "22 of 31 analyzed" is not read as nine left to do.
func TestTheOwnedTierStatesWhatHasNothingToAnalyze(t *testing.T) {
	s := newTestStore(t)
	declareOwner(t, s, "acme")
	enqueueRoot(t, s, "acme/service", "done")
	enqueueRoot(t, s, "acme/docs", "nothing")
	enqueueRoot(t, s, "acme/waiting", "pending")

	counts, err := s.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}
	owned := counts[TierOwned]
	if owned.Repositories != 3 || owned.RepositoriesAnalyzed != 1 || owned.RepositoriesNothingToAnalyze != 1 ||
		owned.NothingToAnalyze != 1 {
		t.Errorf("owned tier %+v, want 3 repositories: 1 analyzed, 1 with nothing to analyze", owned)
	}

	frontier, err := s.FrontierCounts()
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := frontier["nothing"]; !ok || n != 1 {
		t.Errorf("frontier counts %v, want nothing to analyze counted", frontier)
	}
}

// Every state is present in the counts, so "none" is a stated answer rather than a missing key.
func TestFrontierCountsStateNothingToAnalyzeEvenWhenThereIsNone(t *testing.T) {
	s := newTestStore(t)
	frontier, err := s.FrontierCounts()
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := frontier["nothing"]; !ok || n != 0 {
		t.Errorf("frontier counts %v, want nothing to analyze stated as 0", frontier)
	}
}
