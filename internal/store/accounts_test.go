package store

import (
	"testing"
	"time"
)

func verification(owners ...string) Verification {
	v := Verification{
		Login:      "octocat",
		Host:       "github.com",
		VerifiedAt: time.Now().UTC(),
	}
	for _, o := range owners {
		v.Accounts = append(v.Accounts, Account{Owner: o, Kind: "organization", Role: "member"})
	}
	return v
}

// The whole point of verifying. An account can hold a repository that was analyzed and still be
// one nobody here controls: the actions account owns actions/cache, which arrives as a seed, and
// no operator can change it.
func TestInScopeOwnerIsNotOwnedUntilVerified(t *testing.T) {
	s := subgraphFixture(t)

	owned, err := s.OwnedSet()
	if err != nil {
		t.Fatalf("OwnedSet: %v", err)
	}
	if len(owned) != 0 {
		t.Fatalf("nothing was verified, yet %d accounts report as owned", len(owned))
	}

	candidates, err := s.OwnerCandidates()
	if err != nil {
		t.Fatalf("OwnerCandidates: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("no candidates; the fixture holds seed repositories")
	}
	for _, c := range candidates {
		if c.Owned {
			t.Errorf("%s reports as owned before anything was verified", c.Owner)
		}
	}
}

// The Settings table this feeds was headed "Accounts holding analyzed repositories", and on the
// same page two other sections said the declared account had been analyzed zero times while this
// listed 25 against it. The count is seeds — the repositories discovery started from — and an
// account can hold every one of them with nothing analyzed yet.
func TestAnAccountCountsItsSeedsEvenWithNothingAnalyzed(t *testing.T) {
	s := subgraphFixture(t)
	// Enumerated and never analyzed: repository rows, no nodes, no edges, no findings.
	for _, name := range []string{"fresh-org/one", "fresh-org/two"} {
		if _, err := s.UpsertRepository(name, ""); err != nil {
			t.Fatalf("UpsertRepository(%q): %v", name, err)
		}
	}

	candidates, err := s.OwnerCandidates()
	if err != nil {
		t.Fatalf("OwnerCandidates: %v", err)
	}
	var found bool
	for _, c := range candidates {
		if c.Owner != "fresh-org" {
			continue
		}
		found = true
		if c.Repositories != 2 {
			t.Errorf("fresh-org counts %d, want its 2 seeded repositories", c.Repositories)
		}
		if c.Findings != 0 {
			t.Errorf("fresh-org reports %d findings with nothing analyzed", c.Findings)
		}
	}
	if !found {
		t.Error("an account whose repositories are seeded is absent from the candidates")
	}
}

// Verifying an account says who can act. It must not touch how repositories entered the corpus.
func TestVerifyingAnOwnerLeavesRepositoryOriginAlone(t *testing.T) {
	s := subgraphFixture(t)

	before := originCounts(t, s)
	if err := s.ReplaceAccounts(verification("octo-org")); err != nil {
		t.Fatalf("ReplaceAccounts: %v", err)
	}
	after := originCounts(t, s)

	for origin, n := range before {
		if after[origin] != n {
			t.Errorf("origin %s went from %d to %d repositories after verification",
				origin, n, after[origin])
		}
	}
}

func originCounts(t *testing.T, s *Store) map[string]int {
	t.Helper()
	rows, err := s.DB().Query(`SELECT origin, COUNT(*) FROM repository GROUP BY origin`)
	if err != nil {
		t.Fatalf("reading origins: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var origin string
		var n int
		if err := rows.Scan(&origin, &n); err != nil {
			t.Fatalf("scanning origin: %v", err)
		}
		out[origin] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading origins: %v", err)
	}
	return out
}

// GitHub does not distinguish account names by case, so neither may the stored verification.
//
// Asserted against what was stored rather than against OwnedSet, which since 2026-09-07 reads the
// declared scope: verification records what access exists and no longer decides ownership. The
// case rule is what this test is for and it still holds.
func TestOwnedAccountsIgnoreCaseAndDuplicates(t *testing.T) {
	s := subgraphFixture(t)

	if err := s.ReplaceAccounts(verification("Octo-Org", "octo-org", "OCTO-ORG")); err != nil {
		t.Fatalf("ReplaceAccounts: %v", err)
	}
	stored, err := s.Accounts()
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(stored.Accounts) != 1 {
		t.Fatalf("one account arrived three ways, stored %d: %+v",
			len(stored.Accounts), stored.Accounts)
	}
}

// With nothing verified the split has to be invisible: every page reads as it did before.
func TestNothingVerifiedMeansNothingIsDivided(t *testing.T) {
	s := subgraphFixture(t)

	verified, err := s.Verified()
	if err != nil {
		t.Fatalf("Verified: %v", err)
	}
	if verified {
		t.Error("a fresh database reports a verified identity")
	}

	if err := s.ReplaceAccounts(verification("octo-org")); err != nil {
		t.Fatalf("ReplaceAccounts: %v", err)
	}
	if verified, _ = s.Verified(); !verified {
		t.Error("verified an account, still reports none")
	}

	// Forgetting has to return the corpus to undivided, or the feature is one-way.
	if err := s.ForgetAccounts(); err != nil {
		t.Fatalf("ForgetAccounts: %v", err)
	}
	if verified, _ = s.Verified(); verified {
		t.Error("forgot the identity, still reports one")
	}
}

// Re-verifying replaces what was there. An account left behind after the operator lost access to
// it would keep claiming an access check had passed that never ran again.
func TestReVerifyingReplacesTheAccountsRatherThanAddingToThem(t *testing.T) {
	s := subgraphFixture(t)

	if err := s.ReplaceAccounts(verification("octo-org", "old-org")); err != nil {
		t.Fatalf("ReplaceAccounts: %v", err)
	}
	if err := s.ReplaceAccounts(verification("octo-org")); err != nil {
		t.Fatalf("ReplaceAccounts: %v", err)
	}

	stored, err := s.Accounts()
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(stored.Accounts) != 1 || stored.Accounts[0].Owner != "octo-org" {
		t.Errorf("accounts = %+v, want only the latest verification", stored.Accounts)
	}
}

// Ownership must not be assertable. Every row has to carry what established it, so that a claim
// about who can fix a finding can always be traced back to an authenticated answer.
func TestOwnershipCannotBeDeclared(t *testing.T) {
	s := subgraphFixture(t)

	if err := s.ReplaceAccounts(verification("octo-org")); err != nil {
		t.Fatalf("ReplaceAccounts: %v", err)
	}

	var owner, host, verifiedAs, verifiedAt string
	if err := s.DB().QueryRow(
		`SELECT owner, host, verified_as, verified_at FROM abom_account`).
		Scan(&owner, &host, &verifiedAs, &verifiedAt); err != nil {
		t.Fatalf("reading the stored account: %v", err)
	}
	for name, value := range map[string]string{
		"host": host, "verified_as": verifiedAs, "verified_at": verifiedAt,
	} {
		if value == "" {
			t.Errorf("account %s carries no %s, so nothing established it", owner, name)
		}
	}

	// The table that let an account be typed in must not survive alongside the verified one.
	var n int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'abom_owned_org'`).
		Scan(&n); err != nil {
		t.Fatalf("looking for the declaration table: %v", err)
	}
	if n != 0 {
		t.Error("abom_owned_org still exists, so ownership can still be declared")
	}
}

// A database written before ownership was verified holds declarations that cannot be checked
// retroactively. Carrying them forward would silently promote a guess to a measurement.
func TestDeclarationsFromAnOlderDatabaseAreNotCarriedForward(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.DB().Exec(`
		CREATE TABLE IF NOT EXISTS abom_owned_org (owner TEXT PRIMARY KEY, added_at TEXT NOT NULL);
		INSERT INTO abom_owned_org (owner, added_at) VALUES ('microsoft', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seeding an older database: %v", err)
	}

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	owned, err := s.OwnedSet()
	if err != nil {
		t.Fatalf("OwnedSet: %v", err)
	}
	if owned["microsoft"] {
		t.Error("an unverifiable declaration survived the migration as ownership")
	}
	if verified, _ := s.Verified(); verified {
		t.Error("a migrated declaration reports as a verified identity")
	}
}
