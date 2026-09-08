package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// repeated collects a flag given more than once, so `--org a --org b` is two accounts rather than
// the last one winning.
type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

// runOwned declares which accounts the operator owns, which is what bounds discovery.
//
// Separate from `accounts`, which reports what GitHub says you belong to. That is evidence about
// access; this is a statement of responsibility, and conflating them is how a crawl comes to
// treat every organization you are a member of as yours to remediate.
func runOwned(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("own", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	var orgs, users, includes repeated
	fs.Var(&orgs, "org", "organization you own (repeatable)")
	fs.Var(&users, "user", "your own user account (repeatable; opt-in, never assumed)")
	fs.Var(&includes, "include", "owner/repo to restrict an account to (repeatable)")
	clear := fs.Bool("clear", false, "discard the declaration, which stops discovery")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("own: --db is required")
	}

	s, err := store.Open(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	switch {
	case *clear:
		if err := s.SetOwnedScope(nil); err != nil {
			return fmt.Errorf("own: %w", err)
		}
	case len(orgs) > 0 || len(users) > 0:
		accounts, err := declaration(orgs, users, includes)
		if err != nil {
			return fmt.Errorf("own: %w", err)
		}
		if err := s.SetOwnedScope(accounts); err != nil {
			return fmt.Errorf("own: %w", err)
		}
	case len(includes) > 0:
		return errors.New("own: --include needs the account it belongs to, given with --org or --user")
	}

	declared, err := s.OwnedScope()
	if err != nil {
		return fmt.Errorf("own: %w", err)
	}
	writeDeclaredScope(stdout, declared)
	return nil
}

// declaration folds the flags into accounts, refusing an include that belongs to none of them.
//
// An include for an undeclared account would otherwise restrict nothing and read as accepted,
// leaving a typo looking like a configuration.
func declaration(orgs, users, includes repeated) ([]store.DeclaredAccount, error) {
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
			return nil, fmt.Errorf("--include %s names %s, which was not declared with --org or --user",
				in, owner)
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

func writeDeclaredScope(w io.Writer, accounts []store.DeclaredAccount) {
	if len(accounts) == 0 {
		fmt.Fprintln(w, "nothing is declared, so discovery will not start")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ACCOUNT\tKIND\tSTATE\tSCOPE")
	for _, a := range accounts {
		state := "enabled"
		if !a.Enabled {
			state = "off"
		}
		scope := "every repository"
		if a.IncludesOnly {
			scope = "only " + strings.Join(a.Includes, ", ")
			if len(a.Includes) == 0 {
				scope = "nothing (no includes named)"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.Owner, a.Kind, state, scope)
	}
	_ = tw.Flush()
}
