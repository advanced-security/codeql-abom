package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	"github.com/advanced-security/codeql-abom/internal/checks"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// runCheck runs the built checks on private copies of the database, filing results in its own tables.
func runCheck(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	every := fs.Duration("every", 0, "run the checks again at this interval until interrupted; "+
		"0 runs them once")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("check: --db is required")
	}
	s, err := store.Open(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()

	build := checkBuild()
	for {
		runs, err := checks.Run(s, build, os.TempDir())
		printChecks(stdout, build, runs)
		if err != nil {
			return fmt.Errorf("check: %w", err)
		}
		if *every <= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(*every):
		}
	}
}

// checkBuild includes the reach walk's version, which a change to the walk bumps whatever the revision.
func checkBuild() string {
	info, ok := debug.ReadBuildInfo()
	id := identityFrom(info, ok, version, codeqlVersion)
	name := id.Version
	if id.Development {
		name = "development"
	}
	revision := id.Revision
	if revision == "" {
		revision = "unrecorded"
	}
	if id.Dirty != nil && *id.Dirty {
		revision += "+modified"
	}
	return name + " " + revision + " " + graph.ReachMarkVersion
}

func printChecks(w io.Writer, build string, runs []store.CheckRun) {
	fmt.Fprintf(w, "build %s\n", build)
	for _, run := range runs {
		fmt.Fprintf(w, "check %d (%.1fs)\n", run.Check, float64(run.WallMillis)/1000)
		for _, p := range run.Parts {
			skipped := 0
			for _, k := range p.Skipped {
				skipped += k.Count
			}
			disagreed := "unread"
			if p.Disagreed != nil {
				disagreed = fmt.Sprint(*p.Disagreed)
			}
			fmt.Fprintf(w, "  %s: compared %d, disagreed %s, skipped %d\n", p.Part, p.Compared,
				disagreed, skipped)
			for _, k := range p.Skipped {
				fmt.Fprintf(w, "    skipped %d: %s\n", k.Count, k.Reason)
			}
		}
	}
	for _, c := range checks.Catalog {
		if c.BuiltBy != "" {
			fmt.Fprintf(w, "not built: check %d (%s)\n", c.Number, c.BuiltBy)
		}
	}
}
