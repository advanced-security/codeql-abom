package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// runBranches records which branches the declared repositories protect.
//
// A subcommand rather than part of a crawl, deliberately. This is one paginated request per
// repository and, until the live set reads protected branches (`plans/db/commit-aware-graph.md`),
// buys nothing the graph needs, so it is spend an operator chooses; see
// `plans/api/change-ingestion.md`. Nothing calls it on a schedule.
func runBranches(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("branches", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	apiURL := fs.String("api-url", "https://api.github.com", "GitHub API base URL")
	limit := fs.Int("limit", 0, "stop after this many repositories (0: every declared one)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("branches: --db is required")
	}

	s, err := store.Open(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	repos, err := s.OwnedRepositories()
	if err != nil {
		return fmt.Errorf("branches: %w", err)
	}
	if *limit > 0 && *limit < len(repos) {
		repos = repos[:*limit]
	}

	// The cost is stated before it is spent, because the whole reason this is not automatic is
	// that a reader should decide whether the corpus is worth this many requests.
	fmt.Fprintf(stdout, "Asking about %d repositories, one request each.\n", len(repos))

	client := githubClient(ctx, *apiURL, s)
	var asked, protected, branchCount int
	for _, repository := range repos {
		branches, err := client.ProtectedBranches(ctx, repository)
		if err != nil {
			// One unreachable repository must not discard the ones that answered: this is many
			// independent calls and a partial answer is still worth recording.
			fmt.Fprintf(stderr, "%s: %v\n", repository, err)
			continue
		}
		if err := s.SetProtectedBranches(repository, branches); err != nil {
			return fmt.Errorf("branches: %w", err)
		}
		asked++
		branchCount += len(branches)
		if len(branches) > 0 {
			protected++
		}
	}

	// Branch count matters: one repository can have many protected branches.
	fmt.Fprintf(stdout,
		"Asked %d of %d; %d protect at least one branch, %d branches in total.\n",
		asked, len(repos), protected, branchCount)
	fmt.Fprintln(stdout,
		"The rest were not asked, which is not the same as unprotected.")
	return nil
}
