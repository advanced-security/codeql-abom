package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// runStale reports findings no tracked run of their owning suite still reports.
//
// Read-only, and deliberately so: `plans/db/finding-lifecycle.md` sequences the tombstone behind
// a report somebody has read, because a sweep whose scope is only visible after it runs is one
// nobody will run twice.
func runStale(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("stale", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	top := fs.Int("top", 20, "number of findings to list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("stale: --db is required")
	}

	s, err := store.Open(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	report, err := s.StaleFindings()
	if err != nil {
		return fmt.Errorf("stale: %w", err)
	}

	// What it looked at, before what it found. A reader has to be able to tell "nothing is stale"
	// from "nothing has been re-analyzed yet".
	fmt.Fprintf(stdout, "Sightings on record: %d, over %d repositories where a run attributed "+
		"something.\n", report.Sightings, report.RepositoriesWithSightings)
	fmt.Fprintf(stdout, "Findings nothing has claimed to report: %d. These are never stale \u2014 "+
		"absence in a run that could not have reported it is not absence.\n",
		report.FindingsWithoutSighting)

	if report.Sightings == 0 {
		fmt.Fprintln(stdout, "No sightings yet, so nothing can be called stale. Re-analyze, "+
			"then ask again.")
		return nil
	}

	fmt.Fprintf(stdout, "\nStale: %d findings no tracked run of their suite still reports.\n",
		len(report.Stale))
	for i, f := range report.Stale {
		if i >= *top {
			fmt.Fprintf(stdout, "  ... and %d more\n", len(report.Stale)-*top)
			break
		}
		fmt.Fprintf(stdout, "  %s %s (%s:%d)\n", f.Repository, f.RuleID, f.FilePath, f.StartLine)
	}
	return nil
}
