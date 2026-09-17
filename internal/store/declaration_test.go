package store

import (
	"testing"
	"time"
)

// A declaration is the one fact the whole architecture rests on, and the store could not say where
// any of it came from. A corpus once declared `facebook` and `microsoft` for an operator who has
// never owned either, and nothing in the row could distinguish that from a statement they made.

func TestADeclarationRecordsHowItArrived(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "acme", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByInterface); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	got := readScope(t, s)
	if got[0].DeclaredBy != DeclaredByInterface {
		t.Errorf("DeclaredBy = %q, want %q", got[0].DeclaredBy, DeclaredByInterface)
	}
}

// The two write paths are the command line and the interface, and a reader needs to tell them
// apart: one happened at a terminal, the other in a session someone was looking at.
func TestTheCommandLineAndTheInterfaceAreDistinguishable(t *testing.T) {
	s := newTestStore(t)
	account := []DeclaredAccount{{Owner: "acme", Kind: AccountOrganization, Enabled: true}}

	if err := s.SetOwnedScope(account, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	if got := readScope(t, s)[0].DeclaredBy; got != DeclaredByCLI {
		t.Fatalf("DeclaredBy = %q, want %q", got, DeclaredByCLI)
	}

	if err := s.SetOwnedScope(account, DeclaredByInterface); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	if got := readScope(t, s)[0].DeclaredBy; got != DeclaredByInterface {
		t.Errorf("DeclaredBy = %q, want the later write to own the row", got)
	}
}

// Provenance is decided by the code path that ran, never by the payload. A client that could name
// its own source could claim any of them, which is exactly the assertion this column exists to
// stop being unearned.
func TestAClientCannotAssertItsOwnProvenance(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "acme", Kind: AccountOrganization, Enabled: true, DeclaredBy: DeclaredByCLI},
	}, DeclaredByInterface); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	if got := readScope(t, s)[0].DeclaredBy; got != DeclaredByInterface {
		t.Errorf("DeclaredBy = %q, want the write path to decide, not the payload", got)
	}
}

// An unknown source must stay unknown. Labeling pre-existing rows `cli` would invent precisely the
// provenance this is meant to establish.
func TestARowThatPredatesTheColumnClaimsNoProvenance(t *testing.T) {
	s := migratedLegacyStoreWith(t, func(s *Store) {
		if _, err := s.db.Exec(`
			INSERT INTO abom_owned_scope (owner, kind, enabled, includes_only, declared_at)
			VALUES ('facebook', 'organization', 1, 0, '2026-01-01T00:00:00Z')`); err != nil {
			t.Fatalf("seed legacy declaration: %v", err)
		}
	})

	got := readScope(t, s)
	if len(got) != 1 {
		t.Fatalf("want the legacy row preserved, got %+v", got)
	}
	if got[0].DeclaredBy != "" {
		t.Errorf("DeclaredBy = %q, want it empty: nobody knows who wrote this row",
			got[0].DeclaredBy)
	}
}

// `gh` not having answered is not `gh` disagreeing. One is a missing measurement the operator can
// fix with a command; the other is evidence against the declaration.
func TestGhSilenceIsUnknownRatherThanContradiction(t *testing.T) {
	account := DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: true}

	if got := DeclarationState(account, Verification{}); got != DeclarationUnknown {
		t.Errorf("state = %q with no verification, want %q", got, DeclarationUnknown)
	}
}

func TestAnAccountGhListsIsConfirmed(t *testing.T) {
	account := DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: true}
	v := Verification{Login: "operator", Accounts: []Account{{Owner: "acme", Role: RoleAdmin}}}

	if got := DeclarationState(account, v); got != DeclarationConfirmed {
		t.Errorf("state = %q, want %q", got, DeclarationConfirmed)
	}
}

// The state that did not exist. `facebook` rendered identically to an account gh had never been
// asked about, beside an ownership mark that outweighed both. Contradiction needs a token that
// could have seen the account and did not, so the scope is part of the scenario.
func TestAnAccountGhAnsweredWithoutIsContradicted(t *testing.T) {
	account := DeclaredAccount{Owner: "facebook", Kind: AccountOrganization, Enabled: true}
	v := Verification{
		Login:    "operator",
		Scopes:   []string{"read:org"},
		Accounts: []Account{{Owner: "acme", Role: RoleAdmin}},
	}

	if got := DeclarationState(account, v); got != DeclarationContradicted {
		t.Errorf("state = %q, want %q", got, DeclarationContradicted)
	}
}

// GitHub treats account names case-insensitively, and 7 accounts in the reference corpus spell
// themselves both ways. A case-sensitive compare would report a confirmed account as contradicted.
func TestDeclarationStateComparesAccountNamesCaseInsensitively(t *testing.T) {
	account := DeclaredAccount{Owner: "ACME", Kind: AccountOrganization, Enabled: true}
	v := Verification{Login: "operator", Accounts: []Account{{Owner: "acme", Role: RoleAdmin}}}

	if got := DeclarationState(account, v); got != DeclarationConfirmed {
		t.Errorf("state = %q, want %q", got, DeclarationConfirmed)
	}
}

// `user/memberships/orgs` lists what you belong to and never says what you do not, so "absent
// from the list" is only evidence against the operator when the token could have seen the whole
// list. Without `read:org` it sees none of it, and every declared organization would read as
// contradicted — which, once the gate lands, locks an operator out of their own corpus.
func TestATokenThatCannotReadMembershipsNeverContradicts(t *testing.T) {
	account := DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: true}
	blind := Verification{
		Login:    "operator",
		Scopes:   []string{"repo", "workflow"},
		Accounts: []Account{{Owner: "operator", Kind: AccountUser, Role: RoleOwner}},
	}

	if got := DeclarationState(account, blind); got != DeclarationUnknown {
		t.Errorf("state = %q with no read:org, want %q: absence proves nothing here",
			got, DeclarationUnknown)
	}
}

// With the scope, absence is a real answer and has to stay one.
func TestATokenThatCanReadMembershipsStillContradicts(t *testing.T) {
	account := DeclaredAccount{Owner: "facebook", Kind: AccountOrganization, Enabled: true}
	sighted := Verification{
		Login:    "operator",
		Scopes:   []string{"repo", "read:org"},
		Accounts: []Account{{Owner: "acme", Kind: AccountOrganization, Role: RoleAdmin}},
	}

	if got := DeclarationState(account, sighted); got != DeclarationContradicted {
		t.Errorf("state = %q, want %q", got, DeclarationContradicted)
	}
}

// A verification recorded before scopes were captured cannot say whether the list was complete,
// and guessing either way is worse than saying so.
func TestAVerificationWithNoRecordedScopesDoesNotContradict(t *testing.T) {
	account := DeclaredAccount{Owner: "facebook", Kind: AccountOrganization, Enabled: true}
	legacy := Verification{
		Login:    "operator",
		Accounts: []Account{{Owner: "acme", Kind: AccountOrganization, Role: RoleAdmin}},
	}

	if got := DeclarationState(account, legacy); got != DeclarationUnknown {
		t.Errorf("state = %q for a verification with no scopes, want %q", got, DeclarationUnknown)
	}
}

// The scopes survive a round trip, or the question can only be answered while gh is reachable.
func TestTheScopesAVerificationHadAreRecorded(t *testing.T) {
	s := newTestStore(t)
	if err := s.ReplaceAccounts(Verification{
		Login: "operator", Host: "github.com", VerifiedAt: time.Now().UTC(),
		Scopes:   []string{"repo", "read:org"},
		Accounts: []Account{{Owner: "acme", Kind: AccountOrganization, Role: RoleAdmin}},
	}); err != nil {
		t.Fatalf("ReplaceAccounts: %v", err)
	}

	got, err := s.Accounts()
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if !got.CanReadMemberships() {
		t.Errorf("scopes = %v, want read:org to survive the round trip", got.Scopes)
	}
}

func readScope(t *testing.T, s *Store) []DeclaredAccount {
	t.Helper()
	got, err := s.OwnedScope()
	if err != nil {
		t.Fatalf("OwnedScope: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("want a declaration, got none")
	}
	return got
}

// `addedColumns` runs one pass of ALTER TABLE, so every table it names has to exist by then.
// Several tables are created further down `Migrate`, and an entry for one of those fails with
// "no such table" on every fresh database — which is every unit test and every new install.
func TestEveryTableAddedColumnsNamesExistsWhenThePassRuns(t *testing.T) {
	s := newTestStore(t)

	for _, c := range addedColumns {
		var name string
		err := s.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, c.table,
		).Scan(&name)
		if err != nil {
			t.Errorf("addedColumns names %s.%s, but that table does not exist after Migrate: %v",
				c.table, c.column, err)
		}
	}
}
