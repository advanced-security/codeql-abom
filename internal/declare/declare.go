// Package declare reads an untracked local file stating which accounts the operator owns.
//
// Rebuilding a database from nothing is routine during development, and retyping the declaration
// every time is what makes people skip declaring at all. This is a declaration *channel*, never an
// authority: a row it writes is verified at runtime exactly like one typed at the command line, and
// is held back when `gh` contradicts it.
package declare

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// DefaultFileName is looked for in the working directory, and is git-ignored: a declaration is one
// machine's statement of responsibility rather than a property of the repository.
const DefaultFileName = "abom.local.json"

// File is the on-disk shape.
//
// JSON rather than YAML: this module has three direct dependencies and analyzes its own supply
// chain, so a parser pulled in to read three lists would be its own argument against itself.
type File struct {
	Own Own `json:"own"`
}

// Own mirrors the flags `codeql-abom own` takes, so a file and a command line cannot describe
// different things.
type Own struct {
	Orgs     []string `json:"orgs"`
	Users    []string `json:"users"`
	Includes []string `json:"includes"`
}

// Load reads the declaration at path.
//
// An absent file declares nothing and is not an error. A file that cannot be parsed is one, because
// "nobody wrote a declaration" and "the declaration is broken" are opposite states and only the
// second is something an operator can act on.
func Load(path string) ([]store.DeclaredAccount, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("declare: reading %s: %w", path, err)
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	// Unknown keys are refused so a misspelling cannot declare nothing.
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("declare: %s: %w", path, err)
	}

	accounts, err := Accounts(f.Own.Orgs, f.Own.Users, f.Own.Includes)
	if err != nil {
		return nil, fmt.Errorf("declare: %s: %w", path, err)
	}
	return accounts, nil
}

// Accounts folds the three lists into declarations, refusing an include that belongs to none of
// them.
//
// Shared with `codeql-abom own` so that the file and the command line cannot drift into two
// different rules about what an include means.
func Accounts(orgs, users, includes []string) ([]store.DeclaredAccount, error) {
	byOwner := map[string]*store.DeclaredAccount{}
	order := []string{}
	add := func(owner, kind string) {
		key := strings.ToLower(owner)
		if _, ok := byOwner[key]; ok {
			return
		}
		byOwner[key] = &store.DeclaredAccount{Owner: owner, Kind: kind, Enabled: true}
		order = append(order, key)
	}
	for _, o := range orgs {
		add(o, store.AccountOrganization)
	}
	// Named rather than discovered: a personal account is one person's, so it is opted into.
	for _, u := range users {
		add(u, store.AccountUser)
	}

	for _, in := range includes {
		owner, repo, found := strings.Cut(in, "/")
		if !found || owner == "" || repo == "" {
			return nil, fmt.Errorf("%q does not name owner/repo", in)
		}
		a, ok := byOwner[strings.ToLower(owner)]
		if !ok {
			return nil, fmt.Errorf("include %s names %s, which was not declared as an organization or user", in, owner)
		}
		a.IncludesOnly = true
		a.Includes = append(a.Includes, repo)
	}

	out := make([]store.DeclaredAccount, 0, len(order))
	for _, key := range order {
		out = append(out, *byOwner[key])
	}
	return out, nil
}

// Scope is the part of the store Seed needs, kept narrow so seeding cannot grow into a second way
// of writing a declaration.
type Scope interface {
	OwnedScope() ([]store.DeclaredAccount, error)
	SetOwnedScope([]store.DeclaredAccount, string) error
}

// Seed declares accounts only when nothing is declared, and reports whether it did.
//
// Seeding an empty declaration keeps a fresh database pointed at the organization under
// development.
func Seed(s Scope, accounts []store.DeclaredAccount) (bool, error) {
	if len(accounts) == 0 {
		return false, nil
	}
	existing, err := s.OwnedScope()
	if err != nil {
		return false, fmt.Errorf("declare: reading the declaration: %w", err)
	}
	if len(existing) > 0 {
		return false, nil
	}
	if err := s.SetOwnedScope(accounts, store.DeclaredByConfig); err != nil {
		return false, fmt.Errorf("declare: %w", err)
	}
	return true, nil
}
