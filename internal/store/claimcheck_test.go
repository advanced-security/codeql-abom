package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The check says whether the recorded role still supports each declaration, and a role nobody could
// read is "cannot say": it never removes the claim and never contradicts it.
func TestEachDeclarationIsCheckedAgainstTheRoleItClaims(t *testing.T) {
	s := newTestStore(t)
	v := Verification{Login: "octocat", Host: "github.com", VerifiedAt: time.Now().UTC(), Scopes: []string{"read:org"},
		Accounts: []Account{
			{Owner: "octocat", Kind: AccountUser, Role: RoleOwner},
			{Owner: "admin-org", Kind: AccountOrganization, Role: RoleAdmin},
			{Owner: "managed-org", Kind: AccountOrganization, Role: RoleMember, SecurityManager: "yes"},
			{Owner: "member-org", Kind: AccountOrganization, Role: RoleMember, SecurityManager: "no"},
			{Owner: "blind-org", Kind: AccountOrganization, Role: RoleMember, SecurityManager: "unknown",
				SecurityManagerReason: "gh cannot read who manages security in blind-org"},
		}}
	if err := s.ReplaceAccounts(v); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	if err := s.RecordRepositoryRoles("octocat", at, []RepositoryRole{
		{Owner: "blind-org", Repo: "written", Role: "write"},
		{Owner: "member-org", Repo: "maintained", Role: "maintain"},
		{Owner: "member-org", Repo: "written", Role: "write"},
		{Owner: "member-org", Repo: "hidden", Unread: "gh cannot see member-org/hidden"},
	}); err != nil {
		t.Fatalf("RecordRepositoryRoles: %v", err)
	}
	roles, err := s.RepositoryRoles()
	if err != nil {
		t.Fatal(err)
	}
	v, _ = s.Accounts()

	for name, c := range map[string]struct {
		account DeclaredAccount
		state   string
		include map[string]string
	}{
		"an admin's organization": {DeclaredAccount{Owner: "admin-org", Kind: AccountOrganization, Enabled: true}, ClaimSupported, nil},
		"a security manager's":    {DeclaredAccount{Owner: "managed-org", Kind: AccountOrganization, Enabled: true}, ClaimSupported, nil},
		"a member's, whole":       {DeclaredAccount{Owner: "member-org", Kind: AccountOrganization, Enabled: true}, ClaimUnsupported, nil},
		"a member's maintained repository": {DeclaredAccount{Owner: "member-org", Kind: AccountOrganization, Enabled: true,
			IncludesOnly: true, Includes: []string{"maintained"}}, ClaimSupported, map[string]string{"maintained": ClaimSupported}},
		"a member's repositories, one only written": {DeclaredAccount{Owner: "member-org", Kind: AccountOrganization,
			Enabled: true, IncludesOnly: true, Includes: []string{"maintained", "written", "hidden", "never-read"}},
			ClaimUnsupported, map[string]string{"maintained": ClaimSupported, "written": ClaimUnsupported,
				"hidden": ClaimUnknown, "never-read": ClaimUnknown}},
		"a member's repositories, one unread": {DeclaredAccount{Owner: "member-org", Kind: AccountOrganization,
			Enabled: true, IncludesOnly: true, Includes: []string{"maintained", "hidden"}},
			ClaimUnknown, map[string]string{"maintained": ClaimSupported, "hidden": ClaimUnknown}},
		"a member whose security role cannot be read": {DeclaredAccount{Owner: "blind-org", Kind: AccountOrganization, Enabled: true}, ClaimUnknown, nil},
		// A security manager may include anything, so a written repository refutes nothing while the role is unread.
		"repositories of a member whose security role cannot be read": {DeclaredAccount{Owner: "blind-org",
			Kind: AccountOrganization, Enabled: true, IncludesOnly: true, Includes: []string{"written"}},
			ClaimUnknown, map[string]string{"written": ClaimUnsupported}},
		"an account the token could see and does not belong to": {DeclaredAccount{Owner: "elsewhere", Kind: AccountOrganization, Enabled: true}, ClaimUnsupported, nil},
	} {
		got := CheckClaim(c.account, v, roles)
		if got.State != c.state || got.Detail == "" {
			t.Errorf("%s: %+v, want %s with a reason", name, got, c.state)
		}
		includes := map[string]string{}
		for _, i := range got.Includes {
			includes[i.Repository] = i.State
		}
		for repo, want := range c.include {
			if includes[repo] != want {
				t.Errorf("%s: include %s is %q, want %q", name, repo, includes[repo], want)
			}
		}
	}

	if got := CheckClaim(DeclaredAccount{Owner: "admin-org", Kind: AccountOrganization}, Verification{}, roles); got.State != ClaimUnknown {
		t.Errorf("with nothing verified the check is %+v, want cannot say", got)
	}
	other := v
	other.Login = "someone-else"
	got := CheckClaim(DeclaredAccount{Owner: "member-org", Kind: AccountOrganization, IncludesOnly: true,
		Includes: []string{"maintained"}}, other, roles)
	if got.State != ClaimUnknown {
		t.Errorf("roles read for octocat supported %s's claim: %+v", other.Login, got)
	}
	if scope, _ := s.OwnedScope(); len(scope) != 0 {
		t.Errorf("checking a claim declared something: %+v", scope)
	}
}

// Read as an App, an account it is not installed on is "cannot say" because no installation reads a
// person's role there; the reason must say that, not blame a token's memberships that were never read.
func TestAnAccountTheAppIsNotInstalledOnIsCannotSayForThatReason(t *testing.T) {
	for name, accounts := range map[string][]Account{
		"installed elsewhere": {{Owner: "has-ghas", Kind: AccountOrganization, Role: RoleInstallation}},
		"installed nowhere":   {},
	} {
		v := Verification{Login: "abom-has-ghas[bot]", Host: "github.com", VerifiedAt: time.Now().UTC(), Accounts: accounts}
		got := CheckClaim(DeclaredAccount{Owner: "advanced-security", Kind: AccountOrganization, Enabled: true}, v, nil)
		if got.State != ClaimUnknown {
			t.Errorf("%s: %+v, want cannot say", name, got)
		}
		if !strings.Contains(got.Detail, "App is not installed on advanced-security") ||
			strings.Contains(got.Detail, "token") || strings.Contains(got.Detail, "membership") {
			t.Errorf("%s: the reason is %q, want the App's missing installation", name, got.Detail)
		}
	}
}

// verifiedAsMember records octocat as the claim check's fixture does: an admin, a security manager,
// a member who is neither, and a member whose security role could not be read, with the roles read
// on three of member-org's repositories.
func verifiedAsMember(t *testing.T, s *Store) {
	t.Helper()
	if err := s.ReplaceAccounts(Verification{Login: "octocat", Host: "github.com", VerifiedAt: time.Now().UTC(),
		Scopes: []string{"read:org"}, Accounts: []Account{
			{Owner: "octocat", Kind: AccountUser, Role: RoleOwner},
			{Owner: "admin-org", Kind: AccountOrganization, Role: RoleAdmin},
			{Owner: "managed-org", Kind: AccountOrganization, Role: RoleMember, SecurityManager: "yes"},
			{Owner: "member-org", Kind: AccountOrganization, Role: RoleMember, SecurityManager: "no"},
			{Owner: "blind-org", Kind: AccountOrganization, Role: RoleMember, SecurityManager: "unknown",
				SecurityManagerReason: "gh cannot read who manages security in blind-org"},
		}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRepositoryRoles("octocat", time.Now(), []RepositoryRole{
		{Owner: "member-org", Repo: "maintained", Role: "maintain"},
		{Owner: "member-org", Repo: "written", Role: "write"},
		{Owner: "member-org", Repo: "hidden", Unread: "gh cannot see member-org/hidden"},
	}); err != nil {
		t.Fatal(err)
	}
}

// The role rule is the store's, not the page's: a declaration the picker would never offer is refused
// however it arrives, from the command line, the API or a local file. Eligibility is a reading, so a
// role nobody could read makes nothing newly eligible, and the refusal says it could not be read rather
// than that it is lacking. An account GitHub does not report stays declarable by name, since ownership
// is stated rather than inferred.
func TestADeclarationTheRecordedRoleCannotSupportIsRefusedHoweverItArrives(t *testing.T) {
	org := func(owner string, includes ...string) DeclaredAccount {
		return DeclaredAccount{Owner: owner, Kind: AccountOrganization, Enabled: true,
			IncludesOnly: includes != nil, Includes: includes}
	}
	scoped := org("member-org", "maintained")
	scoped.Properties = []PropertyClause{{Terms: []PropertyTerm{{Name: "team", Value: "payments"}}}}
	refused := map[string]struct {
		claim  DeclaredAccount
		reason string
	}{
		"a member's organization, whole": {org("member-org"), "repositories they administer or maintain"},
		"a member's organization, padded to match nothing": {org(" member-org "),
			"repositories they administer or maintain"},
		"a member's repository they only write to": {org("member-org", "maintained", "written"),
			"member-org/written"},
		"a member's property scope":                  {scoped, "repositories they administer or maintain"},
		"a member's repository whose role is unread": {org("member-org", "hidden"), "could not be read"},
		"a member's repository nobody has read":      {org("member-org", "never-read"), "has not been read"},
		"a member whose security role cannot be read": {org("blind-org"),
			"gh cannot read who manages security in blind-org"},
	}
	accepted := map[string]DeclaredAccount{
		"an admin's organization":                       org("admin-org"),
		"a security manager's organization":             org("managed-org"),
		"the operator's own account":                    {Owner: "octocat", Kind: AccountUser, Enabled: true},
		"a member's maintained repository":              org("member-org", "maintained"),
		"an account GitHub does not report":             org("elsewhere"),
		"a member's include list naming nothing so far": org("member-org", []string{}...),
	}
	for _, by := range []string{DeclaredByCLI, DeclaredByInterface, DeclaredByConfig} {
		s := newTestStore(t)
		verifiedAsMember(t, s)
		for name, c := range refused {
			err := s.SetOwnedScope([]DeclaredAccount{c.claim}, by)
			var refusal *DeclarationRefused
			if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, strings.TrimSpace(c.claim.Owner)) ||
				!strings.Contains(refusal.Reason, c.reason) {
				t.Errorf("%s, by %s: %v, want it refused naming %s and %q", name, by, err, c.claim.Owner, c.reason)
			}
			if declared, _ := s.OwnedScope(); len(declared) != 0 {
				t.Errorf("%s, by %s: a refusal declared %+v", name, by, declared)
			}
		}
		for name, account := range accepted {
			if err := s.SetOwnedScope([]DeclaredAccount{account}, by); err != nil {
				t.Errorf("%s, by %s: refused: %v", name, by, err)
			}
			if err := s.SetOwnedScope(nil, by); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Only what a declaration newly claims is judged. A claim made before its role was read, or before
// the role was lost, stays declared through every later save, narrowed or switched off, because the
// check beside a declaration never changes it; claiming more than that is a new claim, and is judged.
func TestAClaimAlreadyDeclaredIsKeptAndOnlyWhatItAddsIsJudged(t *testing.T) {
	s := newTestStore(t)
	whole := DeclaredAccount{Owner: "member-org", Kind: AccountOrganization, Enabled: true}
	admin := DeclaredAccount{Owner: "admin-org", Kind: AccountOrganization, Enabled: true}
	if err := s.SetOwnedScope([]DeclaredAccount{whole}, DeclaredByCLI); err != nil {
		t.Fatalf("with nothing verified, nothing refutes a claim: %v", err)
	}
	verifiedAsMember(t, s)

	off := whole
	off.Enabled = false
	narrowed := DeclaredAccount{Owner: "member-org", Kind: AccountOrganization, Enabled: true,
		IncludesOnly: true, Includes: []string{"written"}}
	for name, declaration := range map[string][]DeclaredAccount{
		"saved again beside another account": {whole, admin},
		"switched off":                       {off},
		"switched on again":                  {whole},
	} {
		if err := s.SetOwnedScope(declaration, DeclaredByInterface); err != nil {
			t.Errorf("%s: %v, want the declared claim kept", name, err)
		}
	}
	if err := s.SetOwnedScope([]DeclaredAccount{narrowed}, DeclaredByInterface); err != nil {
		t.Errorf("narrowed to a repository it already covered: %v, want it kept", err)
	}
	if err := s.SetOwnedScope([]DeclaredAccount{narrowed}, DeclaredByCLI); err != nil {
		t.Errorf("an include declared before its role was read, saved again: %v", err)
	}
	for name, declaration := range map[string]DeclaredAccount{
		"widened back to the whole organization": whole,
		"an include added that the member only writes to": {Owner: "member-org", Kind: AccountOrganization,
			Enabled: true, IncludesOnly: true, Includes: []string{"written", "written-too"}},
	} {
		if declaration.Includes != nil {
			if err := s.RecordRepositoryRoles("octocat", time.Now(), []RepositoryRole{
				{Owner: "member-org", Repo: "written", Role: "write"},
				{Owner: "member-org", Repo: "written-too", Role: "write"},
			}); err != nil {
				t.Fatal(err)
			}
		}
		var refusal *DeclarationRefused
		if err := s.SetOwnedScope([]DeclaredAccount{declaration}, DeclaredByCLI); !errors.As(err, &refusal) {
			t.Errorf("%s: %v, want it refused", name, err)
		}
	}
	if declared, _ := s.OwnedScope(); len(declared) != 1 || len(declared[0].Includes) != 1 {
		t.Errorf("after the refusals the declaration is %+v, want the narrowed one", declared)
	}
}

// The roles read before a declaration is written cover only what it newly includes, so adding them
// replaces an earlier read of the same repository and keeps every other read a refresh recorded.
func TestAddingRolesKeepsTheRestOnRecord(t *testing.T) {
	s := newTestStore(t)
	at := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	if err := s.RecordRepositoryRoles("octocat", at, []RepositoryRole{
		{Owner: "member-org", Repo: "maintained", Role: "maintain"},
		{Owner: "member-org", Repo: "written", Role: "write"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRepositoryRoles("octocat", at.Add(time.Hour), []RepositoryRole{
		{Owner: "member-org", Repo: "written", Role: "maintain"},
		{Owner: "member-org", Repo: "hidden", Unread: "gh cannot see member-org/hidden"},
	}); err != nil {
		t.Fatal(err)
	}
	roles, err := s.RepositoryRoles()
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 3 || roles["member-org/maintained"].Role != "maintain" ||
		roles["member-org/written"].Role != "maintain" || roles["member-org/hidden"].Unread == "" {
		t.Errorf("roles on record %+v, want the earlier read kept and the new reads added", roles)
	}
}

// A row nobody decided is shown so it can be confirmed, and confirming it is a new claim: what an
// unsigned row says was never judged, so it cannot be what a later save claims nothing beyond.
func TestConfirmingARowWrittenAroundTheDecisionPointIsJudgedAsANewClaim(t *testing.T) {
	s := newTestStore(t)
	verifiedAsMember(t, s)
	if _, err := s.db.Exec(`INSERT INTO abom_owned_scope (owner, kind, enabled, includes_only, declared_at)
		VALUES ('member-org', 'organization', 1, 0, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	err := s.SetOwnedScope([]DeclaredAccount{{Owner: "member-org", Kind: AccountOrganization, Enabled: true}}, DeclaredByCLI)

	var refusal *DeclarationRefused
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "repositories they administer or maintain") {
		t.Errorf("confirming a member's undecided whole-organization row: %v; want it refused by role", err)
	}
}
