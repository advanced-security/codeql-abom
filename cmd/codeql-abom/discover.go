package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/discover"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// runDiscover records which repositories in an organization have a CodeQL database, without
// downloading anything yet.
func runDiscover(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	org := fs.String("org", "", "GitHub organization to enumerate (required)")
	language := fs.String("language", "actions", "CodeQL language to look for a database in")
	apiURL := fs.String("api-url", defaultAPIURL(), "GitHub API base URL")
	concurrency := fs.Int("concurrency", defaultConcurrency, "repositories probed at once")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" || *org == "" {
		fs.Usage()
		return errors.New("discover: --db and --org are required")
	}

	s, err := store.Open(*dbPath, store.Options{BulkLoad: true})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	d := &discover.Discoverer{
		Source:      catalog.New(*apiURL, githubToken(), nil),
		Store:       s,
		Language:    *language,
		Concurrency: *concurrency,
		OnEvent:     logFailures(stdout),
	}

	stats, err := d.Run(ctx, *org)
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}
	fmt.Fprintf(stdout, "discover: %d repositories, %d with a %s database, %d failed\n",
		stats.Processed, stats.Rows, *language, stats.Failed)
	return nil
}

// defaultConcurrency bounds simultaneous GitHub API and CodeQL work. Small enough that a
// laptop-class runner is not overwhelmed, large enough that a single slow repository does not
// serialize an entire organization behind it.
const defaultConcurrency = 4

// defaultAPIURL prefers the URL GitHub Actions injects, which is what makes this work
// unchanged against GitHub Enterprise Server.
func defaultAPIURL() string {
	if v := os.Getenv("GITHUB_API_URL"); v != "" {
		return v
	}
	return "https://api.github.com"
}

// githubToken reads the token GitHub Actions provides automatically, falling back to the name
// the gh CLI uses so a local run authenticates the same way.
func githubToken() string {
	if v := os.Getenv("GITHUB_TOKEN"); v != "" {
		return v
	}
	return os.Getenv("GH_TOKEN")
}

// logFailures reports failed jobs as they happen; a run over thousands of repositories that
// only summarizes at the end would surface a repeated failure as a single confusing number.
func logFailures(w io.Writer) func(work.Event) {
	return func(ev work.Event) {
		if ev.Kind == work.EventFailed {
			fmt.Fprintf(w, "  %s: %v\n", ev.Job.Repository, ev.Err)
		}
	}
}
