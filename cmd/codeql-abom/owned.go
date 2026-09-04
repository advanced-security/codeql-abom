package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// runOwned declares which accounts the operator controls, or lists candidates and current
// declarations.
func runOwned(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("owned", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	add := fs.String("add", "", "comma-separated accounts to declare as owned")
	suggest := fs.Bool("suggest", false, "list candidate accounts, ranked by repositories in scope")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("owned: --db is required")
	}

	s, err := store.Open(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	if *add != "" {
		declared, err := addOwnedOrgs(s, *add)
		if err != nil {
			return fmt.Errorf("owned: %w", err)
		}
		fmt.Fprintf(stdout, "owned: declared %s\n", strings.Join(declared, ", "))
	}

	if *suggest {
		candidates, err := s.OwnerCandidates()
		if err != nil {
			return fmt.Errorf("owned: %w", err)
		}
		writeOwnerCandidates(stdout, candidates)
		return nil
	}

	if *add == "" {
		owners, err := s.OwnedOrgs()
		if err != nil {
			return fmt.Errorf("owned: %w", err)
		}
		writeOwnedOrgs(stdout, owners)
	}
	return nil
}

// addOwnedOrgs merges a comma-separated list of accounts into the existing declaration and
// returns the accounts as declared, so the caller can confirm exactly what was recorded.
func addOwnedOrgs(s *store.Store, add string) ([]string, error) {
	existing, err := s.OwnedOrgs()
	if err != nil {
		return nil, fmt.Errorf("reading owned orgs: %w", err)
	}

	seen := make(map[string]bool, len(existing))
	merged := append([]string{}, existing...)
	for _, o := range existing {
		seen[strings.ToLower(o)] = true
	}
	for _, raw := range strings.Split(add, ",") {
		o := strings.TrimSpace(raw)
		if o == "" || seen[strings.ToLower(o)] {
			continue
		}
		seen[strings.ToLower(o)] = true
		merged = append(merged, o)
	}

	if err := s.SetOwnedOrgs(merged); err != nil {
		return nil, fmt.Errorf("declaring owned orgs: %w", err)
	}
	return merged, nil
}

func writeOwnerCandidates(w io.Writer, candidates []store.OwnerCandidate) {
	if len(candidates) == 0 {
		fmt.Fprintln(w, "No owner candidates: no repositories under review yet.")
		return
	}
	fmt.Fprintln(w, "Owner candidates (* already declared):")
	for _, c := range candidates {
		mark := " "
		if c.Owned {
			mark = "*"
		}
		fmt.Fprintf(w, "  %s %-24s %d repositories, %d findings\n",
			mark, c.Owner, c.Repositories, c.Findings)
	}
}

func writeOwnedOrgs(w io.Writer, owners []string) {
	if len(owners) == 0 {
		fmt.Fprintln(w, "owned: no accounts declared; use --suggest to see candidates")
		return
	}
	fmt.Fprintln(w, "Declared owned accounts:")
	for _, o := range owners {
		fmt.Fprintf(w, "  %s\n", o)
	}
}
