package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// maxExpandRounds bounds how many times expand-all reclaims the frontier in one invocation. It
// exists only as a safety net: a healthy run empties the frontier in far fewer rounds, and
// without a cap a queue whose failures keep becoming reclaimable would never let the process
// exit.
const maxExpandRounds = 1000

// staleClaimLease bounds how long a dependency may sit claimed before another run may take it.
// A claim is released only by the process holding it, so a crash needs a time bound; an hour is
// far longer than a clone, database build and query take.
const staleClaimLease = time.Hour

// defaultCacheMaxMB bounds retained checkouts. Measured across twelve real action repositories
// at a mean of 0.18 MiB per commit, tree and .git together, so this holds on the order of ten
// thousand commits — several times the closure of the reference corpus — and the ceiling is what
// the disk cost is bounded by rather than the crawl's appetite.
const defaultCacheMaxMB = 2048

// checkoutCache prepares the directory retained checkouts live in, or nil for no retention.
//
// Outside WorkDir by construction: a scratch directory is removed when the run that made it
// ends, and a cache that does not outlive the round cannot answer the next one.
func checkoutCache(dir string, maxMB int64) (*expand.CheckoutCache, error) {
	if dir == "" {
		return nil, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", dir, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", abs, err)
	}
	return &expand.CheckoutCache{Dir: abs, MaxBytes: maxMB << 20}, nil
}

// roundDidWork reports whether a round moved the queue, which is how expand-all recognizes a
// drained frontier.
//
// Skips count. A round that found every repository already on record settled all of them, and
// reading that as an empty queue would abandon whatever is still queued behind it. Discoveries
// deliberately do not count: they are enqueued by a round that also attempted something.
func roundDidWork(s expand.Stats) bool {
	return s.Processed > 0 || s.Skipped > 0 || s.Failed > 0 || s.Unavailable > 0
}

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
	cacheDir := fs.String("cache-dir", "",
		"retain fetched checkouts here and reuse each commit across references (default: off)")
	cacheMaxMB := fs.Int64("cache-max-mb", defaultCacheMaxMB,
		"ceiling for retained checkouts, least recently used evicted first (0: unbounded)")
	keepArtifacts := fs.Bool("keep-artifacts", false, "keep downloaded databases and results")
	requeueStale := fs.Bool("requeue-stale", false,
		"first requeue analyzed dependencies whose packs or CodeQL have since changed")
	requeueRepo := fs.String("requeue", "",
		"first requeue one repository (owner/name) and run it ahead of the ranking")
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
	reclaimed, err := queue.ReclaimStale(staleClaimLease)
	if err != nil {
		return fmt.Errorf("expand-all: reclaiming abandoned work: %w", err)
	}
	if reclaimed > 0 {
		fmt.Fprintf(stdout, "expand-all: %d dependencies reclaimed from an interrupted run\n", reclaimed)
	}

	seeded, err := expand.SeedFrontier(s, queue)
	if err != nil {
		return fmt.Errorf("expand-all: seeding the frontier: %w", err)
	}
	fmt.Fprintf(stdout, "expand-all: %d dependencies seeded from repositories under review\n", seeded)
	if err := rank(s, queue); err != nil {
		return fmt.Errorf("expand-all: %w", err)
	}

	var pace *work.Limiter
	if *rateLimitPerHour > 0 {
		pace = work.NewLimiter(*rateLimitPerHour)
	}

	w := store.NewWriter(s, 0)
	server := &runner.CLIServer{CodeQLPath: *codeqlPath}
	defer func() { _ = server.Close() }()
	evaluator := &runner.QueryServer{CodeQLPath: *codeqlPath}
	defer func() { _ = evaluator.Close() }()
	r := &runner.Runner{
		CodeQLPath: *codeqlPath, Lock: runner.SharedLock(),
		Server: server, Evaluator: evaluator,
	}
	checkout := &expand.Checkout{}
	cache, err := checkoutCache(*cacheDir, *cacheMaxMB)
	if err != nil {
		return fmt.Errorf("expand-all: %w", err)
	}
	e := &expand.Expander{
		Queue:    queue,
		Store:    s,
		Ingester: ingest.New(s, w),
		Writer:   w,
		Fetcher:  checkout,
		Probe:    checkout,
		Builder:  r,
		CodeQL:   r,
		Analyzer: r,
		// Without this, `expand --query=` cannot fall through to what the tiers are bound to.
		Resolver: r,
		Decode:   expand.Decoder(r.Decode),
		Pace:     pace,
		Cache:    cache,
		OnEvent:  logFailures(stdout),
	}

	if err := requeueSelection(ctx, e, *requeueStale, *requeueRepo, stdout); err != nil {
		return fmt.Errorf("expand-all: %w", err)
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
		total.Skipped += stats.Skipped
		total.Items += stats.Items
		total.Failed += stats.Failed
		total.Unavailable += stats.Unavailable
		total.Discovered += stats.Discovered
		total.Edges += stats.Edges
		total.Reused += stats.Reused
		if !roundDidWork(stats) {
			break
		}
		if err := rank(s, queue); err != nil {
			return fmt.Errorf("expand-all: %w", err)
		}
	}
	fmt.Fprintf(stdout, "expand-all: %d analyses, %d already on record, %d items settled, "+
		"%d failed, %d unavailable, %d newly discovered, %d edges ingested\n",
		total.Processed, total.Skipped, total.Items, total.Failed, total.Unavailable,
		total.Discovered, total.Edges)
	if cache != nil {
		fmt.Fprintf(stdout, "expand-all: %d checkouts served from %s\n", total.Reused, cache.Dir)
	}

	if err := reportWorkLeftWaiting(stdout, queue); err != nil {
		return fmt.Errorf("expand-all: %w", err)
	}

	if err := (&graph.Builder{Store: s}).BuildAll(); err != nil {
		return fmt.Errorf("expand-all: rebuilding aggregates: %w", err)
	}
	if err := queue.Rescore(); err != nil {
		return fmt.Errorf("expand-all: rescoring the frontier: %w", err)
	}
	if err := finalizeStore(s); err != nil {
		return fmt.Errorf("expand-all: %w", err)
	}
	return nil
}

// rank refreshes what the next claim is ordered by.
//
// Commonality is only partly known when a dependency is first discovered, so a score means
// nothing until reach has been derived from the edges ingested so far.
func rank(s *store.Store, queue *frontier.Queue) error {
	if err := (&graph.Builder{Store: s}).BuildBlastRadius(); err != nil {
		return fmt.Errorf("recomputing reach: %w", err)
	}
	if err := queue.Rescore(); err != nil {
		return fmt.Errorf("rescoring the frontier: %w", err)
	}
	return nil
}

// reportWorkLeftWaiting names dependencies still in backoff.
//
// A round claims nothing while every remaining item waits on a retry, which is indistinguishable
// from an empty queue unless it is said out loud.
func reportWorkLeftWaiting(stdout io.Writer, queue *frontier.Queue) error {
	at, ok, err := queue.NextAttemptAt()
	if err != nil || !ok || !at.After(time.Now().UTC()) {
		return err
	}
	stats, err := queue.Stats()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "expand-all: %d dependencies are waiting on a retry, the first at %s\n",
		stats[frontier.StatusPending], at.Local().Format(time.RFC1123))
	return nil
}
