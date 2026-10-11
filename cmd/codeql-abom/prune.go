package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// runPrune removes what the declared scope does not reach.
//
// Explains by default and acts only when asked: a destructive operation whose scope is visible
// only afterwards is one nobody will run twice.
func runPrune(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := databaseFlag(fs)
	apply := fs.Bool("apply", false, "mark what is outside the declared scope, rather than explaining it")
	compact := fs.Bool("compact", false,
		"delete what has already been marked; every open page reloads afterwards")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("prune: --db is required")
	}

	s, err := store.OpenBackend(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", store.DatabaseLabel(*dbPath), err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", store.DatabaseLabel(*dbPath), err)
	}

	if *compact {
		report, err := s.Compact()
		if err != nil {
			return fmt.Errorf("prune: %w", err)
		}
		fmt.Fprintf(stdout, "compacted %d nodes and %d edges; every client will reload\n",
			report.Nodes, report.Edges)
		return nil
	}

	keep, closure, err := (&graph.Builder{Store: s}).KeepList()
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}

	report, err := s.ExplainOutsideClosure(keep)
	if *apply {
		report, err = s.MarkOutsideClosure(keep)
	}
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}

	verb := "would remove"
	if *apply {
		verb = "marked"
	}
	fmt.Fprintf(stdout, "%d repositories declared, %d reachable from them\n",
		closure.Roots(), closure.Size())
	fmt.Fprintf(stdout, "%s %d nodes and %d edges across %d repositories outside that closure\n",
		verb, report.Nodes, report.Edges, report.Repositories)
	if !*apply {
		fmt.Fprintln(stdout, "nothing was changed; re-run with --apply to mark it")
	}
	return nil
}
