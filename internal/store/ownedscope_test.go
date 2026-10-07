package store

import "testing"

// Membership alone is not ownership; the operator must declare responsibility.
func TestADeclaredAccountIsOwnedAndAnUndeclaredOneIsNot(t *testing.T) {
	s := newTestStore(t)
	declare(t, s, DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: true})

	if !ownedRepo(t, s, "acme/service") {
		t.Error("acme is declared, so its repositories are the operator's")
	}
	if ownedRepo(t, s, "docs/handbook") {
		t.Error("docs was never declared and must not count as owned")
	}
}

// GitHub treats account names case-insensitively, so membership must too — the same rule
// `abom_account` already follows.
func TestDeclaredOwnershipIsCaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	declare(t, s, DeclaredAccount{Owner: "Acme", Kind: AccountOrganization, Enabled: true})

	if !ownedRepo(t, s, "acme/service") {
		t.Error("ACME and acme are one account to GitHub and must be one here")
	}
}

// A non-empty include list means only those repositories. Opt-in, because the failure mode of an
// exclude list is that a repository created after it was written is silently in scope.
func TestAnIncludeListAdmitsOnlyWhatItNames(t *testing.T) {
	s := newTestStore(t)
	declare(t, s, DeclaredAccount{
		Owner: "acme-labs", Kind: AccountOrganization, Enabled: true,
		IncludesOnly: true, Includes: []string{"tooling"},
	})

	if !ownedRepo(t, s, "acme-labs/tooling") {
		t.Error("tooling is named by the include list")
	}
	if ownedRepo(t, s, "acme-labs/experiment") {
		t.Error("an account governed by an include list admits nothing the list omits")
	}
}

// Removing the last include must not silently widen an account from two repositories to all of
// them, which is what deriving "is it include-governed" from the list being non-empty would do.
func TestAnEmptyIncludeListScopesToNothingRatherThanEverything(t *testing.T) {
	s := newTestStore(t)
	declare(t, s, DeclaredAccount{
		Owner: "acme-labs", Kind: AccountOrganization, Enabled: true, IncludesOnly: true,
	})

	if ownedRepo(t, s, "acme-labs/anything") {
		t.Error("an include-governed account with an empty list owns nothing")
	}
}

// The operator's own account is inherently one person's and is not what most operators mean by
// "what we own", so it is opted into rather than out of.
func TestADisabledAccountOwnsNothing(t *testing.T) {
	s := newTestStore(t)
	declare(t, s, DeclaredAccount{Owner: "data-douser", Kind: AccountUser, Enabled: false})

	if ownedRepo(t, s, "data-douser/dotfiles") {
		t.Error("a declared but disabled account contributes nothing until it is enabled")
	}
}

// The gate reads this. "Nothing declared" and "declared but all disabled" are the same answer to
// the only question the gate asks, and both must stop a crawl.
func TestScopeIsUndeclaredUntilSomethingIsEnabled(t *testing.T) {
	s := newTestStore(t)
	if declared(t, s) {
		t.Fatal("a fresh store has declared nothing")
	}

	declare(t, s, DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: false})
	if declared(t, s) {
		t.Error("every declared account is disabled, so there is nothing to crawl")
	}

	declare(t, s, DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: true})
	if !declared(t, s) {
		t.Error("an enabled account is a declared scope")
	}
}

// Replacement rather than merge, for the reason ReplaceAccounts gives: an account that accumulated
// would keep counting as the operator's long after they stopped being responsible for it.
func TestDeclaringWholesaleDiscardsWhatPrecededIt(t *testing.T) {
	s := newTestStore(t)
	declare(t, s, DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: true})
	declare(t, s, DeclaredAccount{Owner: "other", Kind: AccountOrganization, Enabled: true})

	if ownedRepo(t, s, "acme/service") {
		t.Error("acme was replaced and must no longer be owned")
	}
	if !ownedRepo(t, s, "other/service") {
		t.Error("other is the current declaration")
	}
}

// The includes travel with the account they belong to, or a reader cannot tell an account scoped
// to two repositories from one scoped to all of them.
func TestTheDeclarationReadsBackWithItsIncludes(t *testing.T) {
	s := newTestStore(t)
	declare(t, s, DeclaredAccount{
		Owner: "acme-labs", Kind: AccountOrganization, Enabled: true,
		IncludesOnly: true, Includes: []string{"tooling", "runners"},
	})

	got, err := s.OwnedScope()
	if err != nil {
		t.Fatalf("OwnedScope: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want one declared account, got %+v", got)
	}
	if len(got[0].Includes) != 2 {
		t.Errorf("includes = %v, want both", got[0].Includes)
	}
	if !got[0].IncludesOnly {
		t.Error("the account is include-governed and must read back that way")
	}
	if got[0].DeclaredAt == "" {
		t.Error("a declaration with no timestamp cannot be told from a default")
	}
}

func TestAnUnknownAccountKindIsRefused(t *testing.T) {
	s := newTestStore(t)
	err := s.SetOwnedScope([]DeclaredAccount{{Owner: "acme", Kind: "team", Enabled: true}}, DeclaredByCLI)
	if err == nil {
		t.Error("a kind that is neither a user nor an organization is a caller mistake")
	}
}

// A repository name with no owner cannot be attributed to anyone.
func TestARepositoryWithNoOwnerIsNotOwned(t *testing.T) {
	s := newTestStore(t)
	declare(t, s, DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: true})

	for _, name := range []string{"", "/", "acme", "/service"} {
		if ownedRepo(t, s, name) {
			t.Errorf("%q names no repository and cannot be owned", name)
		}
	}
}

// Verified membership is evidence about access, never responsibility.
func TestVerifiedMembershipAloneMakesNothingOwned(t *testing.T) {
	s := newTestStore(t)
	verifies(t, s, "docs", "codeql", "github")

	for _, name := range []string{"docs/handbook", "codeql/queries", "github/roadmap"} {
		if ownedRepo(t, s, name) {
			t.Errorf("%s belongs to an account the operator merely belongs to", name)
		}
	}
	owned, err := s.DeclaredOwners()
	if err != nil {
		t.Fatalf("DeclaredOwners: %v", err)
	}
	if len(owned) != 0 {
		t.Errorf("DeclaredOwners = %v with nothing declared, want empty", owned)
	}
}

// Verification without membership scopes leaves declarations unproven, not contradicted.
func TestADeclarationIsOwnedEvenWhenVerificationDisagrees(t *testing.T) {
	s := newTestStore(t)
	verifies(t, s, "docs")
	declare(t, s, DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: true})

	if !ownedRepo(t, s, "acme/service") {
		t.Error("a declared account is owned whether or not gh has confirmed it")
	}
	if ownedRepo(t, s, "docs/handbook") {
		t.Error("a verified account nobody declared is not owned")
	}
	if verified, _ := s.Verified(); !verified {
		t.Error("the gh verification is still recorded; it has simply stopped deciding ownership")
	}
}

// Previewing an unstored declaration and the stored SQL predicate must answer identically.
func TestCoveredByAgreesWithTheStoredScopePredicate(t *testing.T) {
	s := newTestStore(t)
	scope := []DeclaredAccount{
		{Owner: "Acme", Kind: AccountOrganization, Enabled: true},
		{Owner: "acme-labs", Kind: AccountOrganization, Enabled: true,
			IncludesOnly: true, Includes: []string{"Tooling"}},
		{Owner: "dormant", Kind: AccountOrganization, Enabled: false},
		{Owner: "named-nothing", Kind: AccountOrganization, Enabled: true, IncludesOnly: true},
	}
	declare(t, s, scope...)

	repos := []string{
		"acme/service", "ACME/other",
		"acme-labs/tooling", "acme-labs/experiment",
		"dormant/thing", "named-nothing/anything", "docs/handbook",
	}
	for _, r := range repos {
		if _, err := s.UpsertRepository(r, ""); err != nil {
			t.Fatalf("UpsertRepository(%q): %v", r, err)
		}
	}

	fromSQL, err := s.OwnedRepositories()
	if err != nil {
		t.Fatalf("OwnedRepositories: %v", err)
	}
	owned := map[string]bool{}
	for _, r := range fromSQL {
		owned[r] = true
	}

	for _, full := range repos {
		owner, repo, ok := splitRepository(full)
		if !ok {
			t.Fatalf("splitRepository(%q) failed", full)
		}
		if got := CoveredBy(scope, owner, repo, nil); got != owned[full] {
			t.Errorf("%s: CoveredBy = %v, the stored predicate says %v", full, got, owned[full])
		}
	}
}

func declare(t *testing.T, s *Store, accounts ...DeclaredAccount) {
	t.Helper()
	if err := s.SetOwnedScope(accounts, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
}

func ownedRepo(t *testing.T, s *Store, fullName string) bool {
	t.Helper()
	got, err := s.RepositoryIsOwned(fullName)
	if err != nil {
		t.Fatalf("RepositoryIsOwned(%q): %v", fullName, err)
	}
	return got
}

func declared(t *testing.T, s *Store) bool {
	t.Helper()
	got, err := s.OwnedScopeDeclared()
	if err != nil {
		t.Fatalf("OwnedScopeDeclared: %v", err)
	}
	return got
}

// A local file is a third write path, and the store has to name it like the other two. Provenance
// is what lets the interface say a row nobody typed came from a file on this machine.
func TestAConfigFileIsADeclarationSourceTheStoreRecords(t *testing.T) {
	s := newTestStore(t)
	acct := DeclaredAccount{Owner: "has-ghas", Kind: AccountOrganization, Enabled: true}

	if err := s.SetOwnedScope([]DeclaredAccount{acct}, DeclaredByConfig); err != nil {
		t.Fatalf("a local file must be able to declare: %v", err)
	}

	got, err := s.OwnedScope()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].DeclaredBy != DeclaredByConfig {
		t.Errorf("the declaration must record that it came from a file; got %+v", got)
	}
}

// An invented source stays refused, or provenance says nothing about who wrote a row.
func TestAnInventedDeclarationSourceIsRefused(t *testing.T) {
	s := newTestStore(t)
	acct := DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: true}

	if err := s.SetOwnedScope([]DeclaredAccount{acct}, "webhook"); err == nil {
		t.Error("a source the store does not define must be refused")
	}
}

// The owned tier and every per-repository mark ask this, so it must honor an include list where
// the account-level set cannot.
func TestOwnershipIsAnsweredPerRepository(t *testing.T) {
	s := newTestStore(t)
	declare(t, s, DeclaredAccount{Owner: "octo-org", Kind: AccountOrganization, Enabled: true,
		IncludesOnly: true, Includes: []string{"tool"}})

	got, err := s.OwnedAmong([]string{"octo-org/tool", "Octo-Org/APP", "elsewhere/tool"})
	if err != nil {
		t.Fatalf("OwnedAmong: %v", err)
	}
	if !got["octo-org/tool"] {
		t.Error("octo-org/tool is declared and not reported owned")
	}
	if got["octo-org/app"] || got["elsewhere/tool"] {
		t.Errorf("owned %v, want only octo-org/tool", got)
	}

	accounts, err := s.DeclaredOwners()
	if err != nil {
		t.Fatalf("DeclaredOwners: %v", err)
	}
	if !accounts["octo-org"] {
		t.Error("an account restricted to one repository is still a declared account")
	}
}
