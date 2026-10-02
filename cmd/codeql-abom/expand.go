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
	"github.com/advanced-security/codeql-abom/internal/lockfile"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// maxExpandRounds stops a run whose failures keep becoming reclaimable.
const maxExpandRounds = 1000

// staleClaimLease lets another run take work left claimed by a crashed process.
const staleClaimLease = time.Hour

// defaultCacheMaxMB bounds retained checkouts.
const defaultCacheMaxMB = 2048

// checkoutCache prepares the directory retained checkouts live in, or nil for no retention.
//
// It is owner-only and claimed, since it holds fetched private source and is not safe to share
// between concurrent writers.
func checkoutCache(dir string, maxMB int64) (*expand.CheckoutCache, error) {
	if dir == "" {
		return nil, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", dir, err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", abs, err)
	}
	// MkdirAll leaves an existing directory's mode alone.
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, fmt.Errorf("restricting %s: %w", abs, err)
	}
	cache := &expand.CheckoutCache{Dir: abs, MaxBytes: maxMB << 20}
	if err := cache.Claim(); err != nil {
		return nil, err
	}
	return cache, nil
}

// defaultDatabaseCacheMaxMB bounds retained CodeQL databases at 25 GiB.
const defaultDatabaseCacheMaxMB = 25600

// databaseCache prepares the directory retained databases live in, or nil for no retention.
//
// It is owner-only because databases are built from fetched private source.
func databaseCache(dir string, maxMB int64) (*expand.DatabaseCache, error) {
	if dir == "" {
		return nil, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", dir, err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", abs, err)
	}
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, fmt.Errorf("restricting %s: %w", abs, err)
	}
	return &expand.DatabaseCache{Dir: abs, MaxBytes: maxMB << 20}, nil
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

type expandAllConfig struct {
	dbPath             string
	queryPath          string
	queryID            string
	codeqlPath         string
	workDir            string
	concurrency        int
	limit              int
	maxDepth           int
	rateLimitPerHour   int
	cacheDir           string
	cacheMaxMB         int64
	databaseCacheDir   string
	databaseCacheMaxMB int64
	keepArtifacts      bool
	requeueStale       bool
	requeueRepo        string
}

// runExpandAll follows dependencies out from the seed repositories until the frontier is empty,
// then refreshes every aggregate the graph reports on.
func runExpandAll(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg, err := parseExpandAllArgs(args, stderr)
	if err != nil {
		return err
	}

	dir, cleanup, err := workingDirectory(cfg.workDir, "abom-expand-*")
	if err != nil {
		return err
	}
	defer cleanup()

	s, err := openExpandAllStore(cfg)
	if err != nil {
		return err
	}
	defer s.Close()

	queue := &frontier.Queue{Store: s, MaxDepth: cfg.maxDepth}
	if err := prepareExpandAllQueue(s, queue, stdout); err != nil {
		return err
	}

	e, cache, cleanupRunner, err := newExpander(s, queue, cfg, stdout)
	if err != nil {
		return fmt.Errorf("expand-all: %w", err)
	}
	defer cleanupRunner()
	defer cache.Release()

	if err := requeueSelection(ctx, e, cfg.requeueStale, cfg.requeueRepo, stdout); err != nil {
		return fmt.Errorf("expand-all: %w", err)
	}

	total, err := runExpandRounds(ctx, e, expandAllOptions(cfg, dir), s, queue)
	if err != nil {
		return err
	}
	reportExpandAllSummary(stdout, total, cache)

	if err := reportWorkLeftWaiting(stdout, queue); err != nil {
		return fmt.Errorf("expand-all: %w", err)
	}
	return finishExpandAll(s, queue)
}

func openExpandAllStore(cfg expandAllConfig) (*store.Store, error) {
	s, err := store.Open(cfg.dbPath, store.Options{BulkLoad: true})
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", cfg.dbPath, err)
	}
	if err := s.Migrate(); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("migrating %s: %w", cfg.dbPath, err)
	}
	return s, nil
}

func expandAllOptions(cfg expandAllConfig, dir string) expand.Options {
	return expand.Options{
		QueryPath:     cfg.queryPath,
		QueryID:       cfg.queryID,
		WorkDir:       dir,
		Concurrency:   cfg.concurrency,
		Limit:         cfg.limit,
		KeepArtifacts: cfg.keepArtifacts,
	}
}

func reportExpandAllSummary(stdout io.Writer, total expand.Stats, cache *expand.CheckoutCache) {
	fmt.Fprintf(stdout, "expand-all: %d analyses, %d already on record, %d items settled, "+
		"%d failed, %d unavailable, %d newly discovered, %d edges ingested\n",
		total.Processed, total.Skipped, total.Items, total.Failed, total.Unavailable,
		total.Discovered, total.Edges)
	if cache != nil {
		fmt.Fprintf(stdout, "expand-all: %d checkouts served from %s\n", total.Reused, cache.Dir)
	}
}

func finishExpandAll(s *store.Store, queue *frontier.Queue) error {
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

func parseExpandAllArgs(args []string, stderr io.Writer) (expandAllConfig, error) {
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
	databaseCacheDir := fs.String("database-cache-dir", "",
		"retain built CodeQL databases here and analyze them again without extracting (default: off)")
	databaseCacheMaxMB := fs.Int64("database-cache-max-mb", defaultDatabaseCacheMaxMB,
		"ceiling for retained databases, least recently used evicted first (0: unbounded)")
	keepArtifacts := fs.Bool("keep-artifacts", false, "keep downloaded databases and results")
	requeueStale := fs.Bool("requeue-stale", false,
		"first requeue analyzed dependencies whose packs or CodeQL have since changed")
	requeueRepo := fs.String("requeue", "",
		"first requeue one repository (owner/name) and run it ahead of the ranking")
	if err := fs.Parse(args); err != nil {
		return expandAllConfig{}, err
	}
	if *dbPath == "" {
		fs.Usage()
		return expandAllConfig{}, errors.New("expand-all: --db is required")
	}

	return expandAllConfig{
		dbPath:             *dbPath,
		queryPath:          *queryPath,
		queryID:            *queryID,
		codeqlPath:         *codeqlPath,
		workDir:            *workDir,
		concurrency:        *concurrency,
		limit:              *limit,
		maxDepth:           *maxDepth,
		rateLimitPerHour:   *rateLimitPerHour,
		cacheDir:           *cacheDir,
		cacheMaxMB:         *cacheMaxMB,
		databaseCacheDir:   *databaseCacheDir,
		databaseCacheMaxMB: *databaseCacheMaxMB,
		keepArtifacts:      *keepArtifacts,
		requeueStale:       *requeueStale,
		requeueRepo:        *requeueRepo,
	}, nil
}

func prepareExpandAllQueue(s *store.Store, queue *frontier.Queue, stdout io.Writer) error {
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
	requeued, err := s.RequeueLocksReadByAnotherReader(lockfile.Reader)
	if err != nil {
		return fmt.Errorf("expand-all: requeueing locks another reader read: %w", err)
	}
	if requeued > 0 {
		fmt.Fprintf(stdout, "expand-all: %d dependencies requeued to read their lock again\n", requeued)
	}
	if err := rank(s, queue); err != nil {
		return fmt.Errorf("expand-all: %w", err)
	}
	return nil
}

// newExpander is the expander a command analyzes with, the checkout cache it holds, and what
// closes its CodeQL processes. A closure has no queue and passes nil.
func newExpander(
	s *store.Store, queue *frontier.Queue, cfg expandAllConfig, stdout io.Writer,
) (*expand.Expander, *expand.CheckoutCache, func(), error) {
	var pace *work.Limiter
	if cfg.rateLimitPerHour > 0 {
		pace = work.NewLimiter(cfg.rateLimitPerHour)
	}

	w := store.NewWriter(s, 0)
	server := &runner.CLIServer{CodeQLPath: cfg.codeqlPath}
	evaluator := &runner.QueryServer{CodeQLPath: cfg.codeqlPath}
	cleanup := func() {
		_ = evaluator.Close()
		_ = server.Close()
	}
	r := &runner.Runner{
		CodeQLPath: cfg.codeqlPath, Lock: runner.SharedLock(),
		Server: server, Evaluator: evaluator,
	}
	checkout := &expand.Checkout{}
	cache, err := checkoutCache(cfg.cacheDir, cfg.cacheMaxMB)
	if err != nil {
		cleanup()
		return nil, nil, nil, err
	}
	databases, err := databaseCache(cfg.databaseCacheDir, cfg.databaseCacheMaxMB)
	if err != nil {
		cache.Release()
		cleanup()
		return nil, nil, nil, err
	}
	return &expand.Expander{
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
		Resolver:  r,
		Decode:    expand.Decoder(r.Decode),
		Pace:      pace,
		Cache:     cache,
		Databases: databases,
		OnEvent:   logFailures(stdout),
	}, cache, cleanup, nil
}

func runExpandRounds(
	ctx context.Context, e *expand.Expander, opts expand.Options, s *store.Store, queue *frontier.Queue,
) (expand.Stats, error) {
	var total expand.Stats
	for round := 0; round < maxExpandRounds; round++ {
		stats, err := e.Run(ctx, opts)
		if err != nil {
			return expand.Stats{}, fmt.Errorf("expand-all: %w", err)
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
			return expand.Stats{}, fmt.Errorf("expand-all: %w", err)
		}
	}
	return total, nil
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
