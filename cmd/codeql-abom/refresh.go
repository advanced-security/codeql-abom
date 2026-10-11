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

// runRefresh recomputes the derived tables without crawling.
//
// Only `expand-all` refreshed them before, so a corpus that needed a derivation rather than new
// data had to pay for a full crawl to get one — and an aggregate added by an upgrade stayed empty
// until somebody did. Reach is recomputed wholesale, so this is also what fills a table that a
// release introduced.
func runRefresh(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := databaseFlag(fs)
	maxDepth := fs.Int("max-depth", 0, "stop the reach walk at this many hops; 0 means no limit")
	optimize := fs.Bool("optimize", false,
		"also update the query planner's statistics and compact the file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("refresh: --db is required")
	}

	s, err := store.OpenBackend(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", store.DatabaseLabel(*dbPath), err)
	}
	defer s.Close()
	// Migrating first also builds any index the schema has gained since this database was made.
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", store.DatabaseLabel(*dbPath), err)
	}

	before, err := aggregateCounts(s)
	if err != nil {
		return err
	}
	if err := (&graph.Builder{Store: s, MaxDepth: *maxDepth}).RebuildAggregates(); err != nil {
		return fmt.Errorf("refresh: %w", err)
	}
	after, err := aggregateCounts(s)
	if err != nil {
		return err
	}

	// Both halves are printed because a count that did not move is a result, not a non-event.
	for _, name := range aggregateTables {
		fmt.Fprintf(stdout, "%-24s %7d -> %7d\n", name, before[name], after[name])
	}

	if *optimize {
		if err := s.Optimize(); err != nil {
			return fmt.Errorf("refresh: %w", err)
		}
		fmt.Fprintln(stdout, "statistics updated and file compacted")
	}
	_ = ctx
	return nil
}

// aggregateTables are the derived tables RebuildAggregates writes, in the order it writes them.
var aggregateTables = []string{
	"abom_blast_radius", "abom_node_reach", "abom_node_version_reach", "abom_exposure",
}

func aggregateCounts(s store.Backend) (map[string]int, error) {
	return s.AggregateCounts()
}
