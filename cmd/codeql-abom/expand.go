package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/pipeline"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// maxExpandRounds bounds how many times expand-all reclaims the frontier in one invocation. It
// exists only as a safety net: a healthy run empties the frontier in far fewer rounds, and
// without a cap a queue whose failures keep becoming reclaimable would never let the process
// exit.
const maxExpandRounds = 1000

// runExpandAll follows dependencies out from the seed repositories until the frontier is empty,
// then refreshes every aggregate the graph reports on.
func runExpandAll(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("expand-all", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	queryPath := fs.String("query", defaultEdgesQuery, "edges query to run against each database")
	queryID := fs.String("query-id", defaultEdgesQueryID, "ledger identity for the query")
	codeqlPath := fs.String("codeql", "", "codeql CLI to invoke (default: codeql on PATH)")
	workDir := fs.String("work-dir", "", "scratch directory for checkouts (default: a temp dir)")
	concurrency := fs.Int("concurrency", defaultConcurrency, "repositories processed at once")
	limit := fs.Int("limit", 200, "frontier items claimed per round")
	maxDepth := fs.Int("max-depth", frontier.DefaultMaxDepth, "hops beyond a seed repository to follow")
	rateLimitPerHour := fs.Int("rate-limit-per-hour", 0, "cap checkouts per hour (0: unlimited)")
	keepArtifacts := fs.Bool("keep-artifacts", false, "keep downloaded databases and results")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("expand-all: --db is required")
	}

	dir, cleanup, err := workingDirectory(*workDir, "abom-expand-*")
	if err != nil {
		return err
	}
	defer cleanup()

	s, err := store.Open(*dbPath, store.Options{BulkLoad: true})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	queue := &frontier.Queue{Store: s, MaxDepth: *maxDepth}
	seeded, err := expand.SeedFrontier(s, queue)
	if err != nil {
		return fmt.Errorf("expand-all: seeding the frontier: %w", err)
	}
	fmt.Fprintf(stdout, "expand-all: %d dependencies seeded from repositories under review\n", seeded)

	var pace *work.Limiter
	if *rateLimitPerHour > 0 {
		pace = work.NewLimiter(*rateLimitPerHour)
	}

	w := store.NewWriter(s, 0)
	r := &runner.Runner{CodeQLPath: *codeqlPath}
	e := &expand.Expander{
		Queue:    queue,
		Store:    s,
		Ingester: ingest.New(s, w),
		Writer:   w,
		Fetcher:  &expand.Checkout{},
		Builder:  r,
		CodeQL:   r,
		Decode:   expand.Decoder(pipeline.DefaultDecoder(*codeqlPath)),
		Pace:     pace,
		OnEvent:  logFailures(stdout),
	}

	opts := expand.Options{
		QueryPath:     *queryPath,
		QueryID:       *queryID,
		WorkDir:       dir,
		Concurrency:   *concurrency,
		Limit:         *limit,
		KeepArtifacts: *keepArtifacts,
	}

	var total expand.Stats
	for round := 0; round < maxExpandRounds; round++ {
		stats, err := e.Run(ctx, opts)
		if err != nil {
			return fmt.Errorf("expand-all: %w", err)
		}
		total.Processed += stats.Processed
		total.Items += stats.Items
		total.Failed += stats.Failed
		total.Unavailable += stats.Unavailable
		total.Discovered += stats.Discovered
		total.Edges += stats.Edges
		if stats.Processed == 0 && stats.Failed == 0 && stats.Unavailable == 0 {
			break
		}
	}
	fmt.Fprintf(stdout, "expand-all: %d repositories, %d items settled, %d failed, "+
		"%d unavailable, %d newly discovered, %d edges ingested\n",
		total.Processed, total.Items, total.Failed, total.Unavailable, total.Discovered, total.Edges)

	if err := (&graph.Builder{Store: s}).BuildAll(); err != nil {
		return fmt.Errorf("expand-all: rebuilding aggregates: %w", err)
	}
	if err := finalizeStore(s); err != nil {
		return fmt.Errorf("expand-all: %w", err)
	}
	return nil
}
