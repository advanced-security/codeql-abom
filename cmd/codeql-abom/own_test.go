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
