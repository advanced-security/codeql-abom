package store

import "testing"

// The whole point of the declaration. An account can hold a repository that was analysed and
// still be one nobody here controls: the actions account owns actions/cache, which arrives as a
// seed, and no operator can change it.
func TestInScopeOwnerIsNotOwnedUntilDeclared(t *testing.T) {
	s := subgraphFixture(t)

	owned, err := s.OwnedSet()
	if err != nil {
		t.Fatalf("OwnedSet: %v", err)
	}
	if len(owned) != 0 {
		t.Fatalf("nothing was declared, yet %d accounts report as owned", len(owned))
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
			t.Errorf("%s is suggested as owned before anything was declared", c.Owner)
		}
	}
}

// Declaring an account says who can act. It must not touch how repositories entered the corpus.
func TestDeclaringAnOwnerLeavesRepositoryOriginAlone(t *testing.T) {
	s := subgraphFixture(t)

	before := originCounts(t, s)
	if err := s.SetOwnedOrgs([]string{"octo-org"}); err != nil {
		t.Fatalf("SetOwnedOrgs: %v", err)
	}
	after := originCounts(t, s)

	for origin, n := range before {
		if after[origin] != n {
			t.Errorf("origin %s went from %d to %d repositories after a declaration",
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
	return out
}

// GitHub does not distinguish account names by case, so neither may the declaration.
func TestOwnedOrgsIgnoreCaseAndDuplicates(t *testing.T) {
	s := subgraphFixture(t)

	if err := s.SetOwnedOrgs([]string{"Octo-Org", "octo-org", "  ", "OCTO-ORG"}); err != nil {
		t.Fatalf("SetOwnedOrgs: %v", err)
	}
	owners, err := s.OwnedOrgs()
	if err != nil {
		t.Fatalf("OwnedOrgs: %v", err)
	}
	if len(owners) != 1 {
		t.Fatalf("declared one account four ways, stored %d: %v", len(owners), owners)
	}

	owned, err := s.OwnedSet()
	if err != nil {
		t.Fatalf("OwnedSet: %v", err)
	}
	if !owned["octo-org"] {
		t.Error("membership test is case-sensitive; it must not be")
	}
}

// With nothing declared the feature has to be invisible: every page reads as it did before.
func TestNothingDeclaredMeansNothingIsDivided(t *testing.T) {
	s := subgraphFixture(t)

	declared, err := s.Declared()
	if err != nil {
		t.Fatalf("Declared: %v", err)
	}
	if declared {
		t.Error("a fresh database reports a declaration")
	}

	if err := s.SetOwnedOrgs([]string{"octo-org"}); err != nil {
		t.Fatalf("SetOwnedOrgs: %v", err)
	}
	if declared, _ = s.Declared(); !declared {
		t.Error("declared an account, still reports none")
	}

	// Clearing has to return the corpus to undivided, or the feature is one-way.
	if err := s.SetOwnedOrgs(nil); err != nil {
		t.Fatalf("SetOwnedOrgs(nil): %v", err)
	}
	if declared, _ = s.Declared(); declared {
		t.Error("cleared the declaration, still reports one")
	}
}

// The declaration says who can act. Deriving it from anything the corpus already knows would
// make it a measurement, and the whole point is that it cannot be measured: an account can hold
// analysed repositories and still be one nobody here controls.
func TestOwnershipIsDeclaredRatherThanDerived(t *testing.T) {
	s := newTestStore(t)

	cols, err := s.DB().Query(`SELECT name FROM pragma_table_info('abom_owned_org')`)
	if err != nil {
		t.Fatalf("reading columns: %v", err)
	}
	defer cols.Close()

	got := map[string]bool{}
	for cols.Next() {
		var name string
		if err := cols.Scan(&name); err != nil {
			t.Fatalf("scanning column: %v", err)
		}
		got[name] = true
	}
	if err := cols.Err(); err != nil {
		t.Fatalf("reading columns: %v", err)
	}

	// A column tying the declaration to how a repository entered the corpus would let the two
	// notions be read as one.
	for _, forbidden := range []string{"origin", "is_seed", "seed", "derived", "in_scope"} {
		if got[forbidden] {
			t.Errorf("abom_owned_org carries %q, which makes the declaration derivable", forbidden)
		}
	}
	if !got["owner"] {
		t.Error("abom_owned_org has no owner column")
	}
}
