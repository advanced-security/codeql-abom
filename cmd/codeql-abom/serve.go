package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/declare"
	"github.com/advanced-security/codeql-abom/internal/enumerate"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// shutdownGrace bounds how long serve waits for in-flight requests once asked to stop.
const shutdownGrace = 5 * time.Second

// wireEnumeration lets the loop re-read the declared accounts on a period.
//
// A period of zero leaves the loop unable to enumerate, which is what every build before this did.
func wireEnumeration(
	loop *supervisor.Supervisor, srv *serve.Server, s *store.Store, every time.Duration,
) {
	if every <= 0 {
		return
	}
	loop.EnumerateEvery = every
	loop.Enumerate = func(ctx context.Context) (int, error) {
		report, err := enumerate.Run(ctx, enumerate.Options{
			Store:            s,
			Read:             srv.Enumerate,
			Schema:           srv.EnumerateSchema,
			QueueOwned:       srv.QueueOwned,
			FullListingEvery: srv.FullListingEvery,
		})
		if err != nil {
			return 0, err
		}
		if len(report.Failed) > 0 {
			// Named rather than counted: an account nobody can reach is a coverage gap, and
			// "1 failed" sends a reader looking for which.
			return report.Queued, fmt.Errorf("%s could not be read: %s",
				report.Failed[0].Owner, report.Failed[0].Error)
		}
		return report.Queued, nil
	}
}

// shouldResume reports whether discovery starts with the server.
//
// A crawl the operator started survives a restart, and one they paused stays paused: a restart may
// not overturn either decision. `--expand` forces it either way.
func shouldResume(s *store.Store, autostart bool, stdout, stderr io.Writer) bool {
	if autostart {
		return true
	}
	running, known, err := s.DiscoveryIntent()
	if err != nil {
		fmt.Fprintf(stderr, "serve: cannot read whether discovery should be running: %v\n", err)
		return false
	}
	if known && running {
		fmt.Fprintln(stdout, "serve: resuming discovery, which was running when this last stopped")
		return true
	}
	return false
}

// seedDeclaration applies an untracked local declaration, and only when the database holds none.
//
// It declares; it never vouches. What `gh` says about the account still decides whether the
// declaration is honored, so this is a way to avoid retyping rather than a way around verification.
func seedDeclaration(s *store.Store, path string, stderr io.Writer) error {
	accounts, err := declare.Load(path)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	seeded, err := declare.Seed(s, accounts)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	if !seeded {
		return nil
	}
	names := make([]string, 0, len(accounts))
	for _, a := range accounts {
		names = append(names, a.Owner)
	}
	fmt.Fprintf(stderr, "serve: %s declared %s; verification still decides whether it is honored\n",
		path, strings.Join(names, ", "))
	return nil
}

// runServe answers interface queries over HTTP until the context is canceled.
func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	// Untracked, and only ever fills a vacuum. Development rebuilds the database often, and an
	// operator who retypes the declaration every time stops declaring.
	declareFile := fs.String("declare-file", declare.DefaultFileName,
		"local file declaring owned accounts, applied only when nothing is declared")
	addr := fs.String("addr", "localhost:8080", "address to listen on")
	uiDir := fs.String("ui-dir", "", "directory containing the published interface (optional)")
	allowRemote := fs.Bool("allow-remote-control", false,
		"let a non-loopback client start discovery, which runs git and codeql on this machine")
	// Off by default. Route latencies map what exists and what is expensive, and pprof serves
	// goroutine stacks and heap contents; both are fine on a workstation and not by default.
	diagnostics := fs.Bool("diagnostics", false,
		"expose /api/metrics and /debug/pprof/ for local performance analysis")
	apiURL := fs.String("api-url", defaultAPIURL(), "GitHub API base URL")
	// How stale the list of repositories may get. A read is bounded by what moved since the last
	// one rather than by how big the account is; see plans/api/org-enumeration.md.
	enumerateEvery := fs.Duration("enumerate-every", supervisor.DefaultEnumerateEvery,
		"how often the declared accounts are re-read for new repositories (0: never)")
	// Only a full listing can establish that a repository is gone, so sweeping forever keeps dead
	// repositories while looking more current than reading everything would.
	listEvery := fs.Duration("list-every", enumerate.DefaultFullListingEvery,
		"how often every page of an account is read, which is the only way a deletion is seen")
	// Selection is the operator's; detection is at most a default. An empty value prefers GraphQL
	// and falls back to REST, and a pinned route is never second-guessed.
	route := fs.String("read-route", "",
		"which API reads an account: graphql, rest, or empty to prefer graphql and fall back")
	// Named by the operator, discovered by nobody. A dividing line inferred from a repository-name
	// prefix agrees with the organization by coincidence and disagrees the first time somebody
	// creates a repository. Empty means no grouping, which is honest rather than unhelpful. It
	// chooses only what a screen opens on; what a filter means travels in the filter.
	groupProperty := fs.String("group-property", "",
		"repository custom property to group by on screen, for example business_id")
	// Empty by default, and an empty list submits nowhere. A dependency snapshot replaces data on
	// somebody else's repository and cannot be withdrawn, so the targets are named explicitly or
	// this build can only preview.
	submitTargets := fs.String("submit-targets", "",
		"comma-separated repositories that may receive dependency snapshots")
	var d discoveryOptions
	fs.BoolVar(&d.autostart, "expand", false, "start discovery as soon as the server is up")
	// Empty by default: what each tier runs is stored configuration, editable while the server is
	// up, and naming a query here overrides it for every tier.
	fs.StringVar(&d.queryPath, "query", "", "single edges query to run instead of the stored per-tier suites")
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
	fs.DurationVar(&d.advisoryMaxAge, "advisory-max-age", supervisor.DefaultAdvisoryMaxAge,
		"longest the advisory set may go unrefreshed once the queue has drained (0: only on arrival)")
	fs.DurationVar(&d.advisoryMinPeriod, "advisory-min-interval", supervisor.DefaultAdvisoryMinInterval,
		"shortest gap between two advisory refreshes, however much arrives")
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
	if reason := volatileDatabase(*dbPath); reason != "" {
		fmt.Fprintf(stderr, "serve: %s\n", reason)
	}

	s, err := store.Open(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	if err := seedDeclaration(s, *declareFile, stderr); err != nil {
		return err
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
	// Same reasoning, and a sharper failure: the only other caller runs inside a discovery cycle,
	// so a database migrated in place and never crawled serves every boundary filter an empty
	// answer, which reads as "no cross-owner dependencies" rather than "never measured".
	if _, err := s.ClassifyEdgeBoundaries(); err != nil {
		fmt.Fprintf(stderr, "serve: classifying edge boundaries: %v\n", err)
	}
	// Before SeedFrontier, which can only queue the dependencies of a repository that has
	// already been analyzed. These are what get analyzed first.
	if seeded, err := expand.SeedOwnedRepositories(s, loop.Queue); err != nil {
		fmt.Fprintf(stderr, "serve: queueing owned repositories: %v\n", err)
	} else if seeded > 0 {
		fmt.Fprintf(stdout, "serve: %d repositories you own queued for analysis\n", seeded)
	}
	// Seeding is idempotent, and without it a database that has only been built has nothing
	// for the loop to claim however long it runs.
	if seeded, err := expand.SeedFrontier(s, loop.Queue); err != nil {
		fmt.Fprintf(stderr, "serve: seeding the frontier: %v\n", err)
	} else if seeded > 0 {
		fmt.Fprintf(stdout, "serve: %d dependencies seeded from repositories under review\n", seeded)
	}
	// A composite action analyzed only on its default branch contributes nothing to a submission:
	// a child is attributable to a consumer only when the parent was analyzed at the ref that
	// consumer pinned. These are already queued by SeedFrontier; what they lack is priority, and
	// they are the ceiling on how deep any submission can go.
	if pinned, err := expand.ExpediteCompositePins(s, loop.Queue); err != nil {
		fmt.Fprintf(stderr, "serve: prioritizing composite pins: %v\n", err)
	} else if pinned > 0 {
		fmt.Fprintf(stdout,
			"serve: %d composite actions prioritized at the references our workflows pin\n", pinned)
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
		Diagnostics: *diagnostics,
		// Taken from the loop rather than the flag: the flag may be empty, in which case the
		// supervisor made a scratch directory and that is where the checkouts actually are.
		WorkDir: loop.Options.WorkDir,
	}
	// Without this the interface can record a declaration and never act on it: declaring names
	// accounts, and only enumeration turns them into the repositories a report is about.
	reader, err := readClient(ctx, *apiURL, s, *route)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	srv.Enumerate = reader.Sweep
	srv.EnumerateSchema = reader.PropertySchema
	srv.FullListingEvery = *listEvery
	srv.GroupProperty = *groupProperty
	srv.QueueOwned = func() (int, error) { return expand.SeedOwnedRepositories(s, loop.Queue) }
	// The same enumeration the button performs, on a period. A person clicking and a loop on a
	// timer take one path, so a repository created after the last read is not invisible until
	// somebody happens to press it.
	wireEnumeration(loop, srv, s, *enumerateEvery)
	// Preview always works; submitting requires a configured target. A build with no targets can
	// inspect what it would send and cannot write, which is the right default for something that
	// cannot be undone.
	wireSnapshots(srv, s, githubToken(ctx), splitTargets(*submitTargets),
		catalog.New(*apiURL, githubToken(ctx), nil).HeadSHA)
	if *uiDir != "" {
		srv.UI = os.DirFS(*uiDir)
	}
	writeIdentityBanner(stdout, s)

	// Off the startup path deliberately: the walk behind this covers the whole declared closure,
	// and an operator reaching the interface must not wait through it to be told what no longer
	// belongs. Nothing is removed by it — it records what a prune would cost, and the Settings
	// page offers that to somebody who can accept it.
	go srv.CheckStaleness()

	loopCtx, stopLoop := context.WithCancel(ctx)
	defer stopLoop()
	go func() {
		if err := loop.Run(loopCtx); err != nil {
			fmt.Fprintf(stderr, "serve: discovery stopped: %v\n", err)
		}
	}()
	if shouldResume(s, d.autostart, stdout, stderr) {
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
