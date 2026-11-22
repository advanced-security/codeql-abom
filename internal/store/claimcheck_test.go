package store

import (
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
