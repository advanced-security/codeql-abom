package store

import (
	"testing"
)

// The corpus figure and the declared one are independent, and the interface prints one of them
// beside the word "scope". On the reference corpus they are 204 and 0, so a reader who takes the
// first for the second believes 204 repositories are theirs when none have been claimed.
func TestSummaryCountsOwnedRepositoriesApartFromSeeded(t *testing.T) {
	s := advisoryStore(t)
	for _, name := range []string{"octo-org/app", "octo-org/tools", "other-org/thing"} {
		if _, err := s.UpsertRepository(name, ""); err != nil {
			t.Fatalf("UpsertRepository(%q): %v", name, err)
		}
	}
	declareOwner(t, s, "octo-org")

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got.SeedRepos != 3 {
		t.Errorf("SeedRepos = %d, want 3", got.SeedRepos)
	}
	if got.OwnedRepos != 2 {
		t.Errorf("OwnedRepos = %d, want 2: only the declared account's repositories are theirs",
			got.OwnedRepos)
	}
}

// Nothing declared means nothing owned, whatever the corpus holds. This is the reference corpus's
// own state, and the case the interface got wrong.
func TestSummaryOwnsNothingWhenNothingIsDeclared(t *testing.T) {
	s := advisoryStore(t)
	if _, err := s.UpsertRepository("octo-org/app", ""); err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got.SeedRepos != 1 {
		t.Errorf("SeedRepos = %d, want 1", got.SeedRepos)
	}
	if got.OwnedRepos != 0 {
		t.Errorf("OwnedRepos = %d, want 0 when nothing is declared", got.OwnedRepos)
	}
}

// A declaration limited to named repositories owns those and not the rest of the account, or the
// count would contradict the tier and the queue, which honor the include list.
func TestSummaryHonorsAnIncludeListWhenCountingOwnedRepositories(t *testing.T) {
	s := advisoryStore(t)
	for _, name := range []string{"octo-org/app", "octo-org/tools"} {
		if _, err := s.UpsertRepository(name, ""); err != nil {
			t.Fatalf("UpsertRepository(%q): %v", name, err)
		}
	}
	if err := s.SetOwnedScope([]DeclaredAccount{{
		Owner:        "octo-org",
		Kind:         AccountOrganization,
		Enabled:      true,
		IncludesOnly: true,
		Includes:     []string{"app"},
	}}, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got.OwnedRepos != 1 {
		t.Errorf("OwnedRepos = %d, want 1: the include list names one repository", got.OwnedRepos)
	}
}
