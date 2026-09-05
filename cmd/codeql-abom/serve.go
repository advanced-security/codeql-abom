package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// shutdownGrace bounds how long serve waits for in-flight requests once asked to stop.
const shutdownGrace = 5 * time.Second

// runServe answers interface queries over HTTP until the context is canceled.
func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	addr := fs.String("addr", "localhost:8080", "address to listen on")
	uiDir := fs.String("ui-dir", "", "directory containing the published interface (optional)")
	allowRemote := fs.Bool("allow-remote-control", false,
		"let a non-loopback client start discovery, which runs git and codeql on this machine")
	var d discoveryOptions
	fs.BoolVar(&d.autostart, "expand", false, "start discovery as soon as the server is up")
	fs.StringVar(&d.queryPath, "query", defaultEdgesQuery, "edges query to run against each database")
	fs.StringVar(&d.queryID, "query-id", defaultEdgesQueryID, "ledger identity for the query")
	fs.StringVar(&d.suite, "expand-suite", "",
		"query suite to run against each database instead of a single query")
	fs.StringVar(&d.codeqlPath, "codeql", "", "codeql CLI to invoke (default: codeql on PATH)")
	fs.StringVar(&d.workDir, "work-dir", "", "scratch directory for checkouts (default: a temp dir)")
	fs.IntVar(&d.concurrency, "expand-concurrency", defaultConcurrency, "repositories analyzed at once")
	fs.IntVar(&d.threads, "expand-threads", 0,
		"threads per codeql invocation (0: share the machine between concurrent analyses)")
	fs.IntVar(&d.ramMB, "expand-ram", 0, "memory cap per codeql invocation in MB (0: codeql decides)")
	fs.IntVar(&d.limit, "expand-limit", 20, "dependencies claimed per cycle")
	fs.IntVar(&d.maxDepth, "max-depth", frontier.DefaultMaxDepth, "hops beyond a seed repository to follow")
	fs.DurationVar(&d.interval, "expand-interval", supervisor.DefaultInterval,
		"longest a cycle waits before looking for work again")
	fs.DurationVar(&d.recheckTTL, "recheck-ttl", supervisor.DefaultRecheckTTL,
		"how old an analysis must be before its ref is probed for drift")
	fs.DurationVar(&d.lease, "expand-lease", supervisor.DefaultLease,
		"how long a claimed dependency may sit before an interrupted run's work is taken back")
	fs.IntVar(&d.rateLimitPerHour, "rate-limit-per-hour", 0, "cap checkouts per hour (0: unlimited)")
	fs.IntVar(&d.aggregateAfter, "aggregate-after-edges", supervisor.DefaultAfterEdges,
		"dependencies that must arrive before reach is recomputed")
	fs.DurationVar(&d.aggregateMaxAge, "aggregate-max-age", supervisor.DefaultMaxAge,
		"longest reach may go unmeasured, however little arrives")
	fs.DurationVar(&d.aggregateMinPeriod, "aggregate-min-interval", supervisor.DefaultMinInterval,
		"shortest gap between two reach rebuilds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("serve: --db is required")
	}

	s, err := store.Open(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	loop, err := newSupervisor(s, d, stderr)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	// Attribution is two statements and it decides whether a finding is visible at all. Running
	// it here means the count of unplaced findings is honest from the first page load, rather
	// than improving on its own once a cycle happens to run.
	if _, err := s.LinkFindingsToNodes(); err != nil {
		fmt.Fprintf(stderr, "serve: attributing findings: %v\n", err)
	}
	// Seeding is idempotent, and without it a database that has only been built has nothing
	// for the loop to claim however long it runs.
	if seeded, err := expand.SeedFrontier(s, loop.Queue); err != nil {
		fmt.Fprintf(stderr, "serve: seeding the frontier: %v\n", err)
	} else if seeded > 0 {
		fmt.Fprintf(stdout, "serve: %d dependencies seeded from repositories under review\n", seeded)
	}
	// Before anything is served, not on the first cycle: until discovery is started the queue
	// would otherwise report a previous run's claims as work in flight, on a page that says
	// discovery has never started.
	if n, err := loop.Queue.ReclaimOrphaned(); err != nil {
		fmt.Fprintf(stderr, "serve: reclaiming abandoned work: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(stdout, "%s serve: %d dependencies were left claimed by an interrupted run\n",
			time.Now().UTC().Format(time.RFC3339), n)
	}

	srv := &serve.Server{
		Store: s, Discovery: loop, AllowRemoteControl: *allowRemote, Log: stderr,
	}
	if *uiDir != "" {
		srv.UI = os.DirFS(*uiDir)
	}
	writeIdentityBanner(stdout, s)

	loopCtx, stopLoop := context.WithCancel(ctx)
	defer stopLoop()
	go func() {
		if err := loop.Run(loopCtx); err != nil {
			fmt.Fprintf(stderr, "serve: discovery stopped: %v\n", err)
		}
	}()
	if d.autostart {
		if problem := loop.Start(); problem != nil {
			fmt.Fprintf(stderr, "serve: discovery cannot start: %s\n  %s\n",
				problem.Reason, problem.Remedy)
		} else {
			fmt.Fprintln(stdout, "serve: discovery running")
		}
	}

	httpServer := &http.Server{Addr: *addr, Handler: srv.Handler()}

	errCh := make(chan error, 1)
	go func() {
		// Timestamp and pid, because one log file accumulates several runs and the discovery
		// cycle counter restarts at 1 each time: without a boundary the trail cannot be split.
		fmt.Fprintf(stdout, "%s serve: listening on http://%s (pid %d)\n",
			time.Now().UTC().Format(time.RFC3339), *addr, os.Getpid())
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("serve: shutting down: %w", err)
		}
		return nil
	}
}
