package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/lockverify"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// lockVerifier runs GitHub's lock verification against a repository's current commit, fetched
// fresh: the same path for the command and the server.
func lockVerifier(s store.Backend, tokens ghapp.TokenSource) *lockverify.Service {
	return lockVerifierWithPolicy(s, tokens, nil, defaultAPIURL())
}

func lockVerifierWithPolicy(s store.Backend, tokens ghapp.TokenSource,
	policy func(context.Context, string) error, apiURL string,
) *lockverify.Service {
	if policy == nil {
		reader := newReader(context.Background(), apiURL, tokens)
		policy = protectSources(s, reader, os.Stderr).Check
	}
	checkout := &expand.Checkout{Timeout: 5 * time.Minute, Tokens: tokens}
	bindSourcePolicy(checkout, apiURL, policy)
	return &lockverify.Service{
		Store: s,
		Fetch: func(ctx context.Context, dest, owner, repo, commit string) error {
			_, err := checkout.Fetch(ctx, dest, owner, repo, commit)
			return err
		},
		Runner: lockverify.Runner{Timeout: 5 * time.Minute},
	}
}

func runVerifyLock(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("verify-lock", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := databaseFlag(fs)
	repository := fs.String("repository", "", "owner/name whose current commit to verify (required)")
	network := fs.Bool("network", false,
		"re-verify every pin against GitHub rather than only checking coverage offline")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" || *repository == "" {
		fs.Usage()
		return errors.New("verify-lock: --db and --repository are required")
	}
	s, err := store.OpenBackend(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", store.DatabaseLabel(*dbPath), err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", store.DatabaseLabel(*dbPath), err)
	}

	mode := lockverify.ModeLocal
	if *network {
		mode = lockverify.ModeNetwork
	}
	tokens, err := dbReaderTokens(*dbPath)
	if err != nil {
		return fmt.Errorf("verify-lock: %w", err)
	}
	got, err := lockVerifier(s, tokens).Request(ctx, *repository, mode, "cli")
	if err != nil {
		return fmt.Errorf("verify-lock: %w", err)
	}
	fmt.Fprintf(stdout, "%s at %s, %s: %s", got.Repository, got.Commit, got.Mode, got.Outcome)
	if got.ToolVersion != "" {
		fmt.Fprintf(stdout, " (gh actions-lock %s, lockfile %s)", got.ToolVersion, got.LockfileVersion)
	}
	fmt.Fprintln(stdout)
	if got.Error != "" {
		fmt.Fprintf(stdout, "  %s\n", got.Error)
	}
	for _, f := range got.Findings {
		known := ""
		if !f.Known {
			known = " [category not known to this build]"
		}
		fmt.Fprintf(stdout, "  %s %s %s %s: %s%s\n", f.Severity, f.Category, f.Workflow,
			f.Dependency, f.Detail, known)
	}
	return nil
}
