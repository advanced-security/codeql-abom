package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// runBranches records which branches the declared repositories protect, all of them now.
//
// Enumeration records the same answer for each owned repository a listing names, so a full listing
// refreshes every one once a period. This asks every declared repository at once instead: one
// paginated request each, which is why the count is stated before it is spent. Recording an answer
// queues each protected branch, which is what puts its commit in every walk, and removes the row of
// a branch that lost protection.
func runBranches(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("branches", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := databaseFlag(fs)
	apiURL := fs.String("api-url", "https://api.github.com", "GitHub API base URL")
	limit := fs.Int("limit", 0, "stop after this many repositories (0: every declared one)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("branches: --db is required")
	}

	s, err := store.OpenBackend(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", store.DatabaseLabel(*dbPath), err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", store.DatabaseLabel(*dbPath), err)
	}

	repos, err := s.OwnedRepositories()
	if err != nil {
		return fmt.Errorf("branches: %w", err)
	}
	if *limit > 0 && *limit < len(repos) {
		repos = repos[:*limit]
	}

	// The cost is stated before it is spent, so a reader can stop a run over a large estate before
	// it has made every request.
	fmt.Fprintf(stdout, "Asking about %d repositories, one request each.\n", len(repos))

	tokens, err := dbReaderTokens(*dbPath)
	if err != nil {
		return fmt.Errorf("branches: %w", err)
	}
	client := githubClient(ctx, *apiURL, s, tokens)
	var asked, protected, branchCount, queued, removed int
	for _, repository := range repos {
		branches, err := client.ProtectedBranches(ctx, repository)
		if err != nil {
			// One unreachable repository must not discard the ones that answered: this is many
			// independent calls and a partial answer is still worth recording.
			fmt.Fprintf(stderr, "%s: %v\n", repository, err)
			continue
		}
		queue, err := s.SetProtectedBranches(repository, branches)
		if err != nil {
			return fmt.Errorf("branches: %w", err)
		}
		asked++
		branchCount += len(branches)
		if len(branches) > 0 {
			protected++
		}
		queued += queue.Queued
		removed += queue.Removed
	}

	// Branch count matters: one repository can have many protected branches.
	fmt.Fprintf(stdout,
		"Asked %d of %d; %d protect at least one branch, %d branches in total.\n",
		asked, len(repos), protected, branchCount)
	fmt.Fprintf(stdout,
		"Queued %d protected branches for analysis; removed %d that lost protection.\n",
		queued, removed)
	fmt.Fprintln(stdout,
		"The rest were not asked, which is not the same as unprotected.")
	return nil
}
