package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/advanced-security/codeql-abom/internal/declare"
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
	var properties repeated
	fs.Var(&properties, "property",
		`a property scope as JSON, {"org": "...", "match": [{"name": "...", "value": "..."}]} (repeatable; terms in one all hold)`)
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
		if err := s.SetOwnedScope(nil, store.DeclaredByCLI); err != nil {
			return fmt.Errorf("own: %w", err)
		}
	case len(orgs) > 0 || len(users) > 0:
		scopes := make([]declare.PropertyScope, 0, len(properties))
		for _, body := range properties {
			p, err := declare.ParsePropertyScope(body)
			if err != nil {
				return fmt.Errorf("own: %w", err)
			}
			scopes = append(scopes, p)
		}
		accounts, err := declare.Accounts(orgs, users, includes, scopes)
		if err != nil {
			return fmt.Errorf("own: %w", err)
		}
		if err := s.SetOwnedScope(accounts, store.DeclaredByCLI); err != nil {
			return fmt.Errorf("own: %w", err)
		}
	case len(includes) > 0 || len(properties) > 0:
		return errors.New("own: --include and --property need the account they belong to, given with --org or --user")
	}

	declared, err := s.OwnedScope()
	if err != nil {
		return fmt.Errorf("own: %w", err)
	}
	writeDeclaredScope(stdout, declared)
	return nil
}

// restrictedScope says what a restricted account covers: its named repositories, or the ones
// carrying each property scope's terms, and why a held scope counts nothing.
func restrictedScope(a store.DeclaredAccount) string {
	var parts []string
	if len(a.Includes) > 0 {
		parts = append(parts, "only "+strings.Join(a.Includes, ", "))
	}
	for _, c := range a.Properties {
		terms := make([]string, 0, len(c.Terms))
		for _, t := range c.Terms {
			terms = append(terms, t.Name+" = "+t.Value)
		}
		part := "carrying " + strings.Join(terms, " and ")
		if c.Held {
			part += " (held: " + c.HeldReason + ")"
		}
		if len(c.Defaults) > 0 {
			part += " (the default of " + strings.Join(c.Defaults, ", ") +
				": every repository that never chose a value is covered, and every new one)"
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "nothing (no repositories or property scopes named)"
	}
	return strings.Join(parts, "; or ")
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
			scope = restrictedScope(a)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.Owner, a.Kind, state, scope)
	}
	_ = tw.Flush()
}
