package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

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

func ownDB(t *testing.T) string {
	t.Helper()
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
