package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// runAccounts shows which accounts the operator is verified to belong to.
//
// There is deliberately no flag to add one. An account counts because an authenticated gh call
// said so, which is what separates it from a guess about who can fix a finding.
func runAccounts(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("accounts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	refresh := fs.Bool("refresh", false, "ask gh who you are and which accounts you belong to")
	forget := fs.Bool("forget", false, "discard the stored identity")
	suggest := fs.Bool("suggest", false, "list accounts holding repositories in scope")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("accounts: --db is required")
	}

	s, err := store.Open(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	if *forget {
		if err := s.ForgetAccounts(); err != nil {
			return fmt.Errorf("accounts: %w", err)
		}
	}
	if *refresh {
		if err := verifyIdentity(ctx, s); err != nil {
			return fmt.Errorf("accounts: %w", err)
		}
	}

	if *suggest {
		candidates, err := s.OwnerCandidates()
		if err != nil {
			return fmt.Errorf("accounts: %w", err)
		}
		writeOwnerCandidates(stdout, candidates)
		return nil
	}

	current, err := s.Accounts()
	if err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	writeAccounts(stdout, current)
	return nil
}

// verifyIdentity asks gh who the operator is and records what it establishes.
func verifyIdentity(ctx context.Context, s *store.Store) error {
	id, err := ghauth.Verify(ctx, ghauth.Exec)
	if err != nil {
		return err
	}
	accounts := make([]store.Account, 0, len(id.Accounts))
	for _, a := range id.Accounts {
		accounts = append(accounts, store.Account{Owner: a.Owner, Kind: a.Kind, Role: a.Role})
	}
	return s.ReplaceAccounts(store.Verification{
		Login:      id.Login,
		Host:       id.Host,
		VerifiedAt: id.VerifiedAt,
		Accounts:   accounts,
		Scopes:     id.Scopes,
	})
}

func writeAccounts(w io.Writer, v store.Verification) {
	if len(v.Accounts) == 0 {
		fmt.Fprintln(w, "accounts: none verified, so every figure is reported undivided")
		fmt.Fprintln(w, "  run: codeql-abom accounts --db <path> --refresh")
		return
	}
	fmt.Fprintf(w, "Verified as %s on %s at %s\n\n",
		v.Login, v.Host, v.VerifiedAt.Format(time.RFC3339))
	for _, a := range v.Accounts {
		fmt.Fprintf(w, "  %-32s %-14s %s\n", a.Owner, a.Kind, a.Role)
	}
}

// writeIdentityBanner says up front whose accounts the served figures will be split by.
//
// Read rather than re-verified: starting the server must not depend on a network, and silence
// about an unverified identity would leave every "yours" figure on every page unexplained.
func writeIdentityBanner(w io.Writer, s *store.Store) {
	v, err := s.Accounts()
	if err != nil || len(v.Accounts) == 0 {
		fmt.Fprintln(w, "serve: no accounts verified, so every figure is reported undivided; "+
			"run: codeql-abom accounts --refresh")
		return
	}
	fmt.Fprintf(w, "serve: verified as %s on %s, %d accounts are yours\n",
		v.Login, v.Host, len(v.Accounts))
}

func writeOwnerCandidates(w io.Writer, candidates []store.OwnerCandidate) {
	if len(candidates) == 0 {
		fmt.Fprintln(w, "No owner candidates: no repositories under review yet.")
		return
	}
	fmt.Fprintln(w, "Accounts holding repositories in scope (* verified as yours):")
	for _, c := range candidates {
		mark := " "
		if c.Owned {
			mark = "*"
		}
		fmt.Fprintf(w, "  %s %-24s %d repositories, %d findings\n",
			mark, c.Owner, c.Repositories, c.Findings)
	}
}
