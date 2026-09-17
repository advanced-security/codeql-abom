package store

import (
	"testing"
	"time"
)

// sighted records a verification whose token could see the whole organization list, which is the
// only condition under which absence from that list is evidence against a declaration.
func sighted(t *testing.T, s *Store, owners ...string) {
	t.Helper()
	accounts := make([]Account, 0, len(owners))
	for _, o := range owners {
		accounts = append(accounts, Account{Owner: o, Kind: AccountOrganization, Role: RoleAdmin})
	}
	if err := s.ReplaceAccounts(Verification{
		Login: "operator", Host: "github.com", VerifiedAt: time.Now().UTC(),
		Scopes: []string{"repo", "read:org"}, Accounts: accounts,
	}); err != nil {
		t.Fatalf("ReplaceAccounts: %v", err)
	}
}

// The failure this exists to prevent, measured: a corpus declared `facebook` and `microsoft`,
// rooted a bill of materials in 44 of their repositories, and named them as this operator's to
// remediate. `gh` had listed 33 accounts and neither was among them. Ownership is the gateway to
// everything ABOM does on somebody's behalf, so a declaration `gh` answered against roots nothing.
func TestAContradictedDeclarationOwnsNothing(t *testing.T) {
	s := newTestStore(t)
	sighted(t, s, "advanced-security")
	declare(t, s,
		DeclaredAccount{Owner: "facebook", Kind: AccountOrganization, Enabled: true},
		DeclaredAccount{Owner: "advanced-security", Kind: AccountOrganization, Enabled: true},
	)
	for _, r := range []string{"facebook/rocksdb", "advanced-security/dismiss-alerts"} {
		if _, err := s.UpsertRepository(r, ""); err != nil {
			t.Fatalf("UpsertRepository(%q): %v", r, err)
		}
	}

	if ownedRepo(t, s, "facebook/rocksdb") {
		t.Error("a repository of an account gh answered against is presented as the operator's")
	}
	if !ownedRepo(t, s, "advanced-security/dismiss-alerts") {
		t.Error("a confirmed declaration stopped owning its repositories")
	}

	owned, err := s.OwnedRepositories()
	if err != nil {
		t.Fatalf("OwnedRepositories: %v", err)
	}
	if len(owned) != 1 || owned[0] != "advanced-security/dismiss-alerts" {
		t.Errorf("OwnedRepositories = %v, want only the confirmed account's", owned)
	}
}

// Absence of evidence is not evidence against. `user/memberships/orgs` never says "you are not in
// facebook", so a token that cannot read memberships — or a corpus nobody has verified — leaves
// every declaration unproven rather than refuted. Gating on that would lock an operator out of
// their own bill of materials for holding the wrong kind of token.
func TestADeclarationIsHeldOnlyWhenSomethingContradictsIt(t *testing.T) {
	blind := func(t *testing.T, s *Store) {
		t.Helper()
		if err := s.ReplaceAccounts(Verification{
			Login: "operator", Host: "github.com", VerifiedAt: time.Now().UTC(),
			Scopes:   []string{"repo", "workflow"},
			Accounts: []Account{{Owner: "operator", Kind: AccountUser, Role: RoleOwner}},
		}); err != nil {
			t.Fatalf("ReplaceAccounts: %v", err)
		}
	}

	for _, tt := range []struct {
		name   string
		verify func(*testing.T, *Store)
	}{
		{"nobody has verified anything", func(*testing.T, *Store) {}},
		{"the token cannot read memberships", blind},
		{"the verification predates scopes being recorded", func(t *testing.T, s *Store) {
			t.Helper()
			verifies(t, s, "docs")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			tt.verify(t, s)
			declare(t, s, DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: true})
			if _, err := s.UpsertRepository("acme/service", ""); err != nil {
				t.Fatalf("UpsertRepository: %v", err)
			}

			if !ownedRepo(t, s, "acme/service") {
				t.Error("an unproven declaration was held; only a contradicted one may be")
			}
			if !declared(t, s) {
				t.Error("the scope reads as undeclared, so discovery would refuse to start")
			}
		})
	}
}

// A scope whose every account is contradicted is not a scope. Discovery asks one question before
// it starts, and answering yes here would crawl on the strength of a declaration gh refuted.
func TestAScopeOfNothingButContradictionsIsNotDeclared(t *testing.T) {
	s := newTestStore(t)
	sighted(t, s, "advanced-security")
	declare(t, s, DeclaredAccount{Owner: "facebook", Kind: AccountOrganization, Enabled: true})

	if declared(t, s) {
		t.Error("a scope holding only contradicted accounts reads as declared")
	}
}

// The gate is decided in SQL and the state on the page is decided in Go. Two derivations of who
// owns what are two answers waiting to disagree, which is the argument this repository already
// lost once over edge relationships.
func TestTheGateAgreesWithTheDeclarationState(t *testing.T) {
	cases := []struct {
		name    string
		scopes  []string
		listed  []string
		declare string
	}{
		{"confirmed", []string{"read:org"}, []string{"acme"}, "acme"},
		{"contradicted", []string{"read:org"}, []string{"acme"}, "facebook"},
		{"admin:org also sees the list", []string{"admin:org"}, []string{"acme"}, "facebook"},
		{"blind token", []string{"repo"}, []string{"acme"}, "facebook"},
		{"no scopes recorded", nil, []string{"acme"}, "facebook"},
		{"a scope whose name merely contains read:org", []string{"noread:orgx"}, []string{"acme"}, "facebook"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			accounts := make([]Account, 0, len(tt.listed))
			for _, o := range tt.listed {
				accounts = append(accounts, Account{
					Owner: o, Kind: AccountOrganization, Role: RoleAdmin,
				})
			}
			if err := s.ReplaceAccounts(Verification{
				Login: "operator", Host: "github.com", VerifiedAt: time.Now().UTC(),
				Scopes: tt.scopes, Accounts: accounts,
			}); err != nil {
				t.Fatalf("ReplaceAccounts: %v", err)
			}
			declared := DeclaredAccount{Owner: tt.declare, Kind: AccountOrganization, Enabled: true}
			declare(t, s, declared)
			if _, err := s.UpsertRepository(tt.declare+"/thing", ""); err != nil {
				t.Fatalf("UpsertRepository: %v", err)
			}

			verified, err := s.Accounts()
			if err != nil {
				t.Fatalf("Accounts: %v", err)
			}
			wantHeld := DeclarationState(declared, verified) == DeclarationContradicted
			if held := !ownedRepo(t, s, tt.declare+"/thing"); held != wantHeld {
				t.Errorf("the gate holds this declaration = %v, the page says %q",
					held, DeclarationState(declared, verified))
			}
		})
	}
}
