package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Configuring before a crawl is what "pre-seeding" means: a scripted run declares its scope and
// starts, without anyone opening a page.
func TestOwnDeclaresAnOrganization(t *testing.T) {
	db := ownDB(t)
	runOwn(t, "--db", db, "--org", "acme")

	if !ownedIn(t, db, "acme/service") {
		t.Error("acme was declared and its repositories are in scope")
	}
}

// A non-empty include list means only those repositories, so a declaration and its exceptions
// arrive together or the account is briefly scoped to all of itself.
func TestOwnRestrictsAnOrganizationToItsIncludes(t *testing.T) {
	db := ownDB(t)
	runOwn(t, "--db", db, "--org", "acme-labs", "--include", "acme-labs/tooling")

	if !ownedIn(t, db, "acme-labs/tooling") {
		t.Error("tooling is named by the include list")
	}
	if ownedIn(t, db, "acme-labs/experiment") {
		t.Error("an include list admits nothing it omits")
	}
}

// The operator's own account is inherently one person's, so naming it is the opt-in.
func TestOwnTakesThePersonalAccountOnlyWhenNamed(t *testing.T) {
	db := ownDB(t)
	runOwn(t, "--db", db, "--org", "acme")
	if ownedIn(t, db, "data-douser/dotfiles") {
		t.Fatal("no personal account was named")
	}

	runOwn(t, "--db", db, "--org", "acme", "--user", "data-douser")
	if !ownedIn(t, db, "data-douser/dotfiles") {
		t.Error("naming the personal account is the opt-in")
	}
}

// An include naming a repository outside every declared account is a typo that would otherwise
// scope an account to nothing and look like it worked.
func TestOwnRefusesAnIncludeForAnUndeclaredAccount(t *testing.T) {
	db := ownDB(t)
	err := own(t, "--db", db, "--org", "acme", "--include", "other/thing")

	if err == nil {
		t.Fatal("an include for an account that was not declared is a mistake, not a scope")
	}
	if !strings.Contains(err.Error(), "other") {
		t.Errorf("error = %v, want it to name the account that was not declared", err)
	}
}

func TestOwnRefusesAnIncludeThatNamesNoRepository(t *testing.T) {
	db := ownDB(t)
	if err := own(t, "--db", db, "--org", "acme", "--include", "acme"); err == nil {
		t.Error("an include must name owner/repo")
	}
}

// Declaring nothing must not silently clear the scope and stop discovery: an empty command line
// is a request to see the declaration, not to discard it.
func TestOwnWithNoAccountsLeavesTheDeclarationAlone(t *testing.T) {
	db := ownDB(t)
	runOwn(t, "--db", db, "--org", "acme")

	runOwn(t, "--db", db)

	if !ownedIn(t, db, "acme/service") {
		t.Error("listing the declaration changed it")
	}
}

func TestOwnClearsOnlyWhenAskedTo(t *testing.T) {
	db := ownDB(t)
	runOwn(t, "--db", db, "--org", "acme")
	runOwn(t, "--db", db, "--clear")

	if ownedIn(t, db, "acme/service") {
		t.Error("--clear discards the declaration")
	}
}

// Replacement rather than merge: an account left behind would keep counting as the operator's
// after they stopped being responsible for it.
func TestOwnReplacesTheWholeDeclaration(t *testing.T) {
	db := ownDB(t)
	runOwn(t, "--db", db, "--org", "acme")
	runOwn(t, "--db", db, "--org", "other")

	if ownedIn(t, db, "acme/service") {
		t.Error("acme was replaced and is no longer declared")
	}
	if !ownedIn(t, db, "other/service") {
		t.Error("other is the current declaration")
	}
}

func TestOwnListsWhatIsDeclared(t *testing.T) {
	db := ownDB(t)
	runOwn(t, "--db", db, "--org", "acme-labs", "--include", "acme-labs/tooling")

	var out bytes.Buffer
	if err := runOwned(context.Background(), []string{"--db", db}, &out, &out); err != nil {
		t.Fatalf("own --list: %v", err)
	}
	if !strings.Contains(out.String(), "acme-labs") || !strings.Contains(out.String(), "tooling") {
		t.Errorf("output = %q, want the account and what it is restricted to", out.String())
	}
}

// A scope held for want of the organization's schema counts nothing, so the listing says why
// rather than showing a restriction that silently admits no repository.
func TestOwnListsAPropertyScopeAndWhyItIsHeld(t *testing.T) {
	db := ownDB(t)
	runOwn(t, "--db", db, "--org", "acme", "--property",
		`{"org": "acme", "match": [{"name": "team", "value": "a"}, {"name": "tier", "value": "1"}]}`)

	var out bytes.Buffer
	if err := runOwned(context.Background(), []string{"--db", db}, &out, &out); err != nil {
		t.Fatalf("own --list: %v", err)
	}
	if !strings.Contains(out.String(), "team = a and tier = 1") || !strings.Contains(out.String(), "held:") {
		t.Errorf("output = %q, want the scope's terms and why it is held", out.String())
	}
}

// A scope naming the value a repository carries for choosing nothing takes in every repository
// nobody set, and every new one, so the listing says so beside it.
func TestOwnListsAScopeNamingTheDefaultAsSuch(t *testing.T) {
	db := ownDB(t)
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePropertySchema("acme", []store.PropertyDefinition{{Name: "region", ValueType: "string",
		ValuesEditableBy: store.PropertyEditableByOrganization, HasDefault: true, DefaultValue: "emea"}}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	runOwn(t, "--db", db, "--org", "acme", "--property", `{"org": "acme", "match": [{"name": "region", "value": "emea"}]}`)

	var out bytes.Buffer
	if err := runOwned(context.Background(), []string{"--db", db}, &out, &out); err != nil {
		t.Fatalf("own --list: %v", err)
	}
	if !strings.Contains(out.String(), "region = emea") || !strings.Contains(out.String(), "default") ||
		strings.Contains(out.String(), "held") {
		t.Errorf("output = %q, want the scope marked as naming region's default", out.String())
	}
}

// asks answers for GitHub while a test runs: who is declaring, and the role read on each repository.
func asks(t *testing.T, verify func(context.Context) (ghauth.Identity, error), roles map[string]string) {
	t.Helper()
	before := declarer
	t.Cleanup(func() { declarer = before })
	declarer = func(string) (func(context.Context) (ghauth.Identity, error),
		func(context.Context, string, string, string) (string, error), error) {
		return verify, func(_ context.Context, owner, repo, _ string) (string, error) {
			if role, ok := roles[owner+"/"+repo]; ok {
				return role, nil
			}
			return "", errors.New("gh cannot read the role on " + owner + "/" + repo)
		}, nil
	}
}

// unverified is GitHub out of reach, which is every test that is not about who is declaring.
func unverified(context.Context) (ghauth.Identity, error) {
	return ghauth.Identity{}, &ghauth.Problem{Reason: "gh is not authenticated.", Remedy: "Run gh auth login."}
}

func verifiedAs(login string, accounts ...ghauth.Account) func(context.Context) (ghauth.Identity, error) {
	return func(context.Context) (ghauth.Identity, error) {
		return ghauth.Identity{Login: login, Host: "github.com", VerifiedAt: time.Now().UTC(),
			Scopes: []string{"read:org"}, Accounts: accounts}, nil
	}
}

// The command line holds a declaration to the rule the page offers by, judged on whoever is declaring
// now: a member may declare only the repositories they administer or maintain, by name, read from
// GitHub before anything is written.
func TestOwnRefusesWhatTheRoleOfWhoeverIsDeclaringCannotSupport(t *testing.T) {
	db := ownDB(t)
	asks(t, verifiedAs("octocat",
		ghauth.Account{Owner: "admin-org", Kind: "organization", Role: "admin"},
		ghauth.Account{Owner: "member-org", Kind: "organization", Role: "member", SecurityManager: "no"},
	), map[string]string{"member-org/maintained": "maintain", "member-org/written": "write"})

	for args, reason := range map[string]string{
		"--org member-org": "repositories they administer or maintain",
		"--org member-org --include member-org/maintained --include member-org/written": "member-org/written",
	} {
		err := own(t, append([]string{"--db", db}, strings.Fields(args)...)...)
		if err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("own %s: %v, want it refused naming %q", args, err, reason)
		}
		if ownedIn(t, db, "member-org/maintained") {
			t.Fatalf("own %s was refused and still declared member-org", args)
		}
	}
	runOwn(t, "--db", db, "--org", "member-org", "--include", "member-org/maintained", "--org", "admin-org")
	if !ownedIn(t, db, "member-org/maintained") || !ownedIn(t, db, "admin-org/anything") {
		t.Error("an admin's organization and a member's maintained repository are declarable")
	}
}

// What was verified last may have been somebody else. The command line asks who is declaring before
// it writes, as the page asks when it opens, so an admin's record does not vouch for a member.
func TestOwnJudgesWhoeverIsDeclaringNowNotWhoeverWasVerifiedLast(t *testing.T) {
	db := ownDB(t)
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceAccounts(store.Verification{Login: "an-admin", Host: "github.com", VerifiedAt: time.Now().UTC(),
		Accounts: []store.Account{{Owner: "acme", Kind: store.AccountOrganization, Role: store.RoleAdmin}}}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	asks(t, verifiedAs("a-member",
		ghauth.Account{Owner: "acme", Kind: "organization", Role: "member", SecurityManager: "no"}), nil)

	if err := own(t, "--db", db, "--org", "acme"); err == nil {
		t.Fatal("a member declared acme whole on an admin's record")
	}
	if ownedIn(t, db, "acme/service") {
		t.Error("the refused declaration was written")
	}
}

// GitHub out of reach is noted, and the declaration is judged against what was last verified, as the
// page judges it when a refresh fails; with nothing verified, nothing refutes it.
func TestOwnWithGitHubOutOfReachIsJudgedAgainstTheRecord(t *testing.T) {
	db := ownDB(t)
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceAccounts(store.Verification{Login: "octocat", Host: "github.com", VerifiedAt: time.Now().UTC(),
		Accounts: []store.Account{{Owner: "acme-labs", Kind: store.AccountOrganization, Role: store.RoleMember,
			SecurityManager: "no"}}}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	var out bytes.Buffer
	err = runOwned(context.Background(), []string{"--db", db, "--org", "acme-labs"}, &out, &out)
	if err == nil || ownedIn(t, db, "acme-labs/tooling") {
		t.Errorf("a member's organization was declared with GitHub out of reach: %v", err)
	}
	if !strings.Contains(out.String(), "gh is not authenticated") {
		t.Errorf("own did not say why it judged against the record:\n%s", out.String())
	}
	runOwn(t, "--db", db, "--org", "acme")
	if !ownedIn(t, db, "acme/service") {
		t.Error("an account nothing on record reports was refused")
	}
}

// A declaration own accepts can still be one discovery cannot act on: an account a whole membership
// list leaves out roots nothing, and one the App is not installed on cannot be listed. Both used to
// print "enabled" and nothing else, so a scripted run declared an organization nothing would crawl.
// Said on stderr, because scripts read the table on stdout.
func TestOwnSaysWhichDeclarationsDiscoveryCannotActOn(t *testing.T) {
	for _, c := range []struct {
		name, args, cannot, can, why string
		verify                       func(context.Context) (ghauth.Identity, error)
	}{
		{
			name: "a membership list that leaves the account out", args: "--org admin-org --org elsewhere",
			cannot: "elsewhere", can: "admin-org", why: "octocat",
			verify: verifiedAs("octocat", ghauth.Account{Owner: "admin-org", Kind: "organization", Role: "admin"}),
		},
		{
			name: "an App not installed on the account", args: "--org has-ghas --org advanced-security",
			cannot: "advanced-security", can: "has-ghas", why: "abom-has-ghas[bot]",
			verify: func(context.Context) (ghauth.Identity, error) {
				return ghauth.Identity{Login: "abom-has-ghas[bot]", Host: "github.com", VerifiedAt: time.Now().UTC(),
					Accounts: []ghauth.Account{{Owner: "has-ghas", Kind: "organization", Role: "installation"}}}, nil
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := ownDB(t)
			asks(t, c.verify, nil)

			for _, args := range []string{c.args, ""} {
				var stdout, stderr bytes.Buffer
				argv := append([]string{"--db", db}, strings.Fields(args)...)
				if err := runOwned(context.Background(), argv, &stdout, &stderr); err != nil {
					t.Fatalf("own %s: %v", args, err)
				}
				if !strings.HasPrefix(stdout.String(), "ACCOUNT") || strings.Contains(stdout.String(), "own:") {
					t.Errorf("own %s changed what scripts read on stdout:\n%s", args, stdout.String())
				}
				note := stderr.String()
				if !strings.Contains(note, c.cannot) || !strings.Contains(note, c.why) {
					t.Errorf("own %s did not say discovery cannot act on %s, or why:\n%s", args, c.cannot, note)
				}
				if strings.Contains(note, c.can+" ") || strings.Contains(note, c.can+":") {
					t.Errorf("own %s named %s, which discovery can act on:\n%s", args, c.can, note)
				}
			}
		})
	}
}

// ownDB is a migrated database for own and accounts to run against, with GitHub answering that
// nobody could be verified until the test says otherwise with asks, so nothing reaches GitHub.
func ownDB(t *testing.T) string {
	t.Helper()
	asks(t, unverified, nil)
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	s.Close()
	return path
}

func own(t *testing.T, args ...string) error {
	t.Helper()
	var out bytes.Buffer
	return runOwned(context.Background(), args, &out, &out)
}

func runOwn(t *testing.T, args ...string) {
	t.Helper()
	if err := own(t, args...); err != nil {
		t.Fatalf("own %v: %v", args, err)
	}
}

func ownedIn(t *testing.T, db, fullName string) bool {
	t.Helper()
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	got, err := s.RepositoryIsOwned(fullName)
	if err != nil {
		t.Fatalf("RepositoryIsOwned: %v", err)
	}
	return got
}

// A scripted run declares a property scope exactly as the file does: one JSON object per flag,
// because a property value may hold any character a delimiter would split on.
func TestOwnDeclaresAPropertyScope(t *testing.T) {
	db := ownDB(t)
	runOwn(t, "--db", db, "--org", "acme", "--property",
		`{"org": "acme", "match": [{"name": "team", "value": "a,b=c:d"}]}`)

	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	declared, err := s.OwnedScope()
	if err != nil {
		t.Fatal(err)
	}
	if len(declared) != 1 || len(declared[0].Properties) != 1 ||
		declared[0].Properties[0].Terms[0].Value != "a,b=c:d" {
		t.Errorf("declared %+v, want acme with the scope team=a,b=c:d", declared)
	}
	for _, bad := range []string{
		`{"org": "acme", "match": [`,
		`{"org": "acme", "match": [{"name": "team", "value": "a"}], "note": "x"}`,
		`{"org": "acme", "match": [{"name": "team", "valu": "a"}]}`,
	} {
		if err := own(t, "--db", db, "--org", "acme", "--property", bad); err == nil {
			t.Errorf("--property %s was accepted", bad)
		}
	}
	if err := own(t, "--db", db, "--property", `{"org": "acme", "match": [{"name": "team", "value": "a"}]}`); err == nil {
		t.Error("--property without the account it belongs to was accepted")
	}
}
