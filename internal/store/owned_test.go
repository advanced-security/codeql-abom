package store

import "testing"

// ownedRow queues a dependency belonging to a named account, which is what decides its tier
// regardless of how far from a seed it was reached.
func ownedRow(t *testing.T, st *Store, key, owner string, depth int) {
	t.Helper()
	if _, err := st.DB().Exec(`
		INSERT INTO abom_frontier (node_key, owner, repo, depth, status, score, enqueued_at,
			analysis_count, resolved_sha)
		VALUES (?, ?, ?, ?, 'done', 1, '2026-01-01T00:00:00Z', 1, 'sha')`,
		key, owner, key, depth); err != nil {
		t.Fatalf("inserting %s: %v", key, err)
	}
}

// declares states that the accounts are the operator's, which is what decides the owned tier.
func declares(t *testing.T, st *Store, owners ...string) {
	t.Helper()
	accounts := make([]DeclaredAccount, 0, len(owners))
	for _, o := range owners {
		accounts = append(accounts, DeclaredAccount{
			Owner: o, Kind: AccountOrganization, Enabled: true,
		})
	}
	if err := st.SetOwnedScope(accounts, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
}

func verifies(t *testing.T, st *Store, owners ...string) {
	t.Helper()
	accounts := make([]Account, 0, len(owners))
	for _, o := range owners {
		accounts = append(accounts, Account{Owner: o, Kind: "organization", Role: "member"})
	}
	if err := st.ReplaceAccounts(Verification{
		Login: "octocat", Host: "github.com", Accounts: accounts,
	}); err != nil {
		t.Fatalf("ReplaceAccounts: %v", err)
	}
}

// What a reader can do about a dependency is the whole point of the split, and owning it is a
// different answer from naming it. Depth cannot express that: a dependency you own is reached at
// whatever distance the graph happens to place it.
func TestOwnershipDecidesTheTierRegardlessOfDepth(t *testing.T) {
	st := newTestStore(t)
	declares(t, st, "octo-org")
	ownedRow(t, st, "near", "octo-org", 1)
	ownedRow(t, st, "far", "octo-org", 4)
	ownedRow(t, st, "theirs", "someone-else", 1)

	counts, err := st.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}
	if counts[TierOwned].Total != 2 {
		t.Errorf("owned = %d, want both of the operator's own regardless of depth",
			counts[TierOwned].Total)
	}
	if counts[TierDirect].Total != 1 {
		t.Errorf("direct = %d, want only the dependency nobody here owns", counts[TierDirect].Total)
	}
}

// The columns are exclusive, so a dependency counted in two of them makes the totals exceed the
// queue and the cost stated on a re-analyze button becomes a lie.
func TestEveryQueuedDependencyLandsInExactlyOneTier(t *testing.T) {
	st := newTestStore(t)
	declares(t, st, "octo-org")
	ownedRow(t, st, "mine-near", "octo-org", 1)
	ownedRow(t, st, "mine-far", "octo-org", 3)
	ownedRow(t, st, "direct", "someone-else", 1)
	ownedRow(t, st, "transitive", "someone-else", 2)

	counts, err := st.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}
	total := counts[TierOwned].Total + counts[TierDirect].Total + counts[TierTransitive].Total
	if total != 4 {
		t.Errorf("the tiers hold %d of 4 dependencies, so they overlap or drop one", total)
	}
}

// GitHub treats account names case-insensitively, so a differently-cased owner is the same owner
// and must not fall out of the tier the operator can act on.
func TestOwnershipIgnoresCase(t *testing.T) {
	st := newTestStore(t)
	declares(t, st, "Octo-Org")
	ownedRow(t, st, "mine", "octo-org", 1)

	counts, err := st.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}
	if counts[TierOwned].Total != 1 {
		t.Errorf("owned = %d, want the differently-cased account to match", counts[TierOwned].Total)
	}
}

// Requeueing has to select the same rows the count promised, or the button spends a different
// amount of work than the number beside it.
func TestRequeueingTheOwnedTierTakesWhatItCounted(t *testing.T) {
	st := newTestStore(t)
	declares(t, st, "octo-org")
	ownedRow(t, st, "mine", "octo-org", 3)
	ownedRow(t, st, "theirs", "someone-else", 3)

	n, err := st.RequeueTier(TierOwned)
	if err != nil {
		t.Fatalf("RequeueTier: %v", err)
	}
	if n != 1 {
		t.Errorf("requeued %d, want only the owned dependency", n)
	}
	if got := statusOfKey(t, st, "theirs"); got != "done" {
		t.Errorf("a transitive dependency was requeued as owned (%q)", got)
	}
}

// With nothing verified the operator owns nothing that anything established, so the tier is empty
// rather than absent from the map: a caller asking for the count of a tier that exists gets zero,
// and it is the interface that decides not to draw a column it cannot justify.
func TestWithNoVerifiedAccountsNothingIsOwned(t *testing.T) {
	st := newTestStore(t)
	ownedRow(t, st, "near", "octo-org", 1)
	ownedRow(t, st, "far", "octo-org", 4)

	counts, err := st.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}
	if counts[TierOwned].Total != 0 {
		t.Errorf("owned = %d with nothing verified, want 0", counts[TierOwned].Total)
	}
	if counts[TierDirect].Total != 1 || counts[TierTransitive].Total != 1 {
		t.Errorf("direct/transitive = %d/%d, want the split to read as it did before accounts",
			counts[TierDirect].Total, counts[TierTransitive].Total)
	}
}
