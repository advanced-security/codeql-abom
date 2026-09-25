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

// A route is worth acting on because it starts somewhere you can change. The corpus figure and
// the owned one are independent for the reason the repository counts are, and the interface
// prints one of them under "routes from something you own".
func TestSummaryCountsOwnedExposuresApartFromEveryExposure(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/init", "v3", 10)
	advisoryAgainst(t, s, "GHSA-owned", "high", "github/codeql-action", "3.0", "")
	exposeRepo(t, s, "octo-org/app", "GHSA-owned", "github/codeql-action/init", 1)
	exposeRepo(t, s, "other-org/thing", "GHSA-owned", "github/codeql-action/init", 2)
	declareOwner(t, s, "octo-org")

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got.Exposures != 2 {
		t.Errorf("Exposures = %d, want both routes the corpus holds", got.Exposures)
	}
	if got.OwnedExposures != 1 {
		t.Errorf("OwnedExposures = %d, want 1: only one route starts in a declared repository",
			got.OwnedExposures)
	}
	if got.OwnedDirectExposures != 1 {
		t.Errorf("OwnedDirectExposures = %d, want 1: the owned route is a direct use",
			got.OwnedDirectExposures)
	}
}

// The state of the restored reference corpus: 253 routes across 115 repositories, none of them
// declared. Reporting the corpus figure as the operator's told them they had 253 routes to act on
// from the zero repositories they own.
func TestSummaryOwnsNoExposureWhenNothingIsDeclared(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "other-org/thing", "github/codeql-action/init", "v3", 10)
	advisoryAgainst(t, s, "GHSA-undeclared", "high", "github/codeql-action", "3.0", "")
	exposeRepo(t, s, "other-org/thing", "GHSA-undeclared", "github/codeql-action/init", 1)

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got.Exposures == 0 {
		t.Fatal("the corpus holds no route, so this proves nothing")
	}
	if got.OwnedExposures != 0 || got.OwnedDirectExposures != 0 {
		t.Errorf("owned exposures = %d (%d direct) with nothing declared, want 0",
			got.OwnedExposures, got.OwnedDirectExposures)
	}
}

// The headline count, and the same claim in a different word: the Dashboard prints it as findings
// "in what you run". A finding in a repository nobody declared is not in what you run.
func TestSummaryCountsOwnedFindingsApartFromEveryFinding(t *testing.T) {
	s := advisoryStore(t)
	for _, repo := range []string{"octo-org/app", "other-org/thing"} {
		id, err := s.UpsertRepository(repo, "")
		if err != nil {
			t.Fatalf("UpsertRepository(%q): %v", repo, err)
		}
		if _, err := s.AddFindings([]Finding{{
			RuleID: "actions/unpinned-tag", RepositoryID: id,
			FilePath: ".github/workflows/ci.yml", StartLine: 10,
		}}); err != nil {
			t.Fatalf("AddFindings(%q): %v", repo, err)
		}
	}
	declareOwner(t, s, "octo-org")

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got.Findings != 2 {
		t.Errorf("Findings = %d, want both the corpus holds", got.Findings)
	}
	if got.OwnedFindings != 1 {
		t.Errorf("OwnedFindings = %d, want 1: one finding is in a declared repository",
			got.OwnedFindings)
	}
}

// The headline says how bad as well as how many, over the same findings it counts.
func TestSummarySaysAtWhatSeverityTheOwnedFindingsAre(t *testing.T) {
	s := advisoryStore(t)
	for repo, scores := range map[string][]*float64{
		"octo-org/app":    {score(9.3), score(5.0), nil},
		"other-org/thing": {score(9.8)},
	} {
		id, err := s.UpsertRepository(repo, "")
		if err != nil {
			t.Fatalf("UpsertRepository(%q): %v", repo, err)
		}
		for i, sc := range scores {
			if _, err := s.AddFindings([]Finding{{RuleID: "r/" + itoa(i), RepositoryID: id,
				FilePath: ".github/workflows/ci.yml", StartLine: 10 + i, SecuritySeverity: sc,
			}}); err != nil {
				t.Fatalf("AddFindings(%q): %v", repo, err)
			}
		}
	}
	declareOwner(t, s, "octo-org")

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if want := (SeverityCounts{Critical: 1, Medium: 1, Unknown: 1}); got.OwnedSeverity != want {
		t.Errorf("OwnedSeverity = %+v, want %+v over the declared repository's three", got.OwnedSeverity, want)
	}
}

// The guard that would have caught this class at any scope: a figure reported as the operator's
// cannot exceed the corpus it is drawn from, and with nothing declared it cannot be anything but
// zero. Seed and owned were the same set in the previous corpus, so nothing could disagree.
func TestNoOwnedFigureOutrunsTheCorpusItIsDrawnFrom(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/init", "v3", 10)
	advisoryAgainst(t, s, "GHSA-guard", "high", "github/codeql-action", "3.0", "")
	exposeRepo(t, s, "octo-org/app", "GHSA-guard", "github/codeql-action/init", 1)
	exposeRepo(t, s, "other-org/thing", "GHSA-guard", "github/codeql-action/init", 1)
	for _, repo := range []string{"octo-org/app", "other-org/thing"} {
		id, err := s.UpsertRepository(repo, "")
		if err != nil {
			t.Fatalf("UpsertRepository(%q): %v", repo, err)
		}
		if _, err := s.AddFindings([]Finding{{
			RuleID: "actions/unpinned-tag", RepositoryID: id,
			FilePath: ".github/workflows/ci.yml", StartLine: 10,
		}}); err != nil {
			t.Fatalf("AddFindings(%q): %v", repo, err)
		}
	}

	for _, declared := range []string{"", "octo-org"} {
		if declared != "" {
			declareOwner(t, s, declared)
		}
		got, err := s.Summary()
		if err != nil {
			t.Fatalf("Summary: %v", err)
		}
		for _, pair := range []struct {
			name         string
			owned, whole int
		}{
			{"repositories", got.OwnedRepos, got.SeedRepos},
			{"findings", got.OwnedFindings, got.Findings},
			{"exposures", got.OwnedExposures, got.Exposures},
			{"direct exposures", got.OwnedDirectExposures, got.DirectExposures},
		} {
			if pair.owned > pair.whole {
				t.Errorf("declared %q: owned %s = %d, more than the %d the corpus holds",
					declared, pair.name, pair.owned, pair.whole)
			}
			if declared == "" && pair.owned != 0 {
				t.Errorf("nothing declared, yet owned %s = %d", pair.name, pair.owned)
			}
		}
	}
}
