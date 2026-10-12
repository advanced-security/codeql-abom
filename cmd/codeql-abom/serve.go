package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/compare"
	"github.com/advanced-security/codeql-abom/internal/coordination"
	"github.com/advanced-security/codeql-abom/internal/declare"
	"github.com/advanced-security/codeql-abom/internal/enumerate"
	"github.com/advanced-security/codeql-abom/internal/events"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/layout"
	"github.com/advanced-security/codeql-abom/internal/lockfile"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/setup"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
	"github.com/advanced-security/codeql-abom/internal/toolchain"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// shutdownGrace bounds how long serve waits for in-flight requests once asked to stop.
const shutdownGrace = 5 * time.Second

// poolFlushEvery is how often what the pools spent is written, and so how stale a meter can be.
const poolFlushEvery = 30 * time.Second

var _ serve.QueryTester = (*runner.Runner)(nil)

var _ serve.ASTPrinter = (*runner.Runner)(nil)

// wireEnumeration lets the loop re-read declared accounts on a period.
// wireTrees lets the crawl recognize a commit's content from one tree read instead of a clone.
// A read is not retried after rate limiting: the clone it falls back to costs less than the wait.
func wireTrees(ctx context.Context, loop *supervisor.Supervisor, apiURL string, tokens ghapp.TokenSource) {
	client := newReader(ctx, apiURL, tokens)
	client.MaxRetries = 0
	client.REST = processPools.Pool(work.PoolFetching, work.UnitREST)
	loop.Expander.Trees = expand.GitHubTrees{Client: client}
}

func wireEnumeration(
	loop *supervisor.Supervisor, srv *serve.Server, s store.Backend, every time.Duration,
) {
	if every <= 0 {
		return
	}
	loop.EnumerateEvery = every
	loop.Enumerate = func(ctx context.Context, ask supervisor.Ask) (int, error) {
		report, err := enumerate.Run(ctx, enumerate.Options{
			Store:            s,
			Read:             srv.Enumerate,
			Schema:           srv.EnumerateSchema,
			Branches:         srv.EnumerateBranches,
			Setup:            srv.EnumerateSetup,
			Configurations:   srv.EnumerateConfigurations,
			QueueOwned:       srv.QueueOwned,
			FullListingEvery: srv.FullListingEvery,
			Owners:           ask.Owners,
			Full:             ask.Full,
			Repositories:     ask.Repositories,
			ReadRepository:   srv.EnumerateRepository,
		})
		if err != nil {
			return 0, err
		}
		if len(report.Failed) > 0 {
			return report.Queued, fmt.Errorf("%s could not be read: %s",
				report.Failed[0].Owner, report.Failed[0].Error)
		}
		return report.Queued, nil
	}
}

// prepareQueue puts initial owned, discovered and priority work in the queue.
func prepareQueue(s store.Backend, queue *frontier.Queue, stdout, stderr io.Writer) {
	if seeded, err := expand.SeedOwnedRepositories(s, queue); err != nil {
		fmt.Fprintf(stderr, "serve: queueing owned repositories: %v\n", err)
	} else if seeded > 0 {
		fmt.Fprintf(stdout, "serve: %d repositories you own queued for analysis\n", seeded)
	}
	if seeded, err := expand.SeedFrontier(s, queue); err != nil {
		fmt.Fprintf(stderr, "serve: seeding the frontier: %v\n", err)
	} else if seeded > 0 {
		fmt.Fprintf(stdout, "serve: %d dependencies seeded from repositories under review\n", seeded)
	}
	if requeued, err := s.RequeueLocksReadByAnotherReader(lockfile.Reader); err != nil {
		fmt.Fprintf(stderr, "serve: requeueing locks another reader read: %v\n", err)
	} else if requeued > 0 {
		fmt.Fprintf(stdout, "serve: %d dependencies requeued to read their lock again\n", requeued)
	}
	if pinned, err := expand.ExpediteCompositePins(s, queue); err != nil {
		fmt.Fprintf(stderr, "serve: prioritizing composite pins: %v\n", err)
	} else if pinned > 0 {
		fmt.Fprintf(stdout,
			"serve: %d composite actions prioritized at the references our workflows pin\n", pinned)
	}
}

// reclaimAbandonedWork settles claims left by a stopped server before serving.
func reclaimAbandonedWork(s store.Backend, queue *frontier.Queue, stdout, stderr io.Writer) {
	if n, err := queue.ReclaimOrphaned(); err != nil {
		fmt.Fprintf(stderr, "serve: reclaiming abandoned work: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(stdout, "%s serve: %d dependencies were left claimed by an interrupted run\n",
			time.Now().UTC().Format(time.RFC3339), n)
	}
	if n, err := s.InterruptAbandonedRuns(); err != nil {
		fmt.Fprintf(stderr, "serve: marking abandoned analysis runs: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(stdout, "serve: %d analysis runs were interrupted when the server last stopped\n", n)
	}
	if n, err := s.InterruptAbandonedComparisons(); err != nil {
		fmt.Fprintf(stderr, "serve: marking abandoned comparisons: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(stdout, "serve: %d comparisons were interrupted when the server last stopped\n", n)
	}
	if n, err := s.InterruptAuthoringSessions(); err != nil {
		fmt.Fprintf(stderr, "serve: marking abandoned authoring sessions: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(stdout, "serve: %d authoring sessions were interrupted when the server last stopped\n", n)
	}
}

// migratePolicyScopes writes retired policy scopes into predicates for this grouping key.
func migratePolicyScopes(s store.Backend, unitProperty string, stdout, stderr io.Writer) {
	report, err := s.MigratePolicyScopes(unitProperty)
	if err != nil {
		fmt.Fprintf(stderr, "serve: migrating policy scopes: %v\n", err)
		return
	}
	if len(report.Migrated) > 0 {
		fmt.Fprintf(stdout, "serve: %d policies had their scope written into their predicate: %s\n",
			len(report.Migrated), strings.Join(report.Migrated, ", "))
	}
	left := make([]string, 0, len(report.Left))
	for name := range report.Left {
		left = append(left, name)
	}
	sort.Strings(left)
	for _, name := range left {
		fmt.Fprintf(stderr, "serve: policy %q keeps a retired scope and is refused until migrated: %s\n",
			name, report.Left[name])
	}
}

// shouldResume reports whether discovery starts with the server.
//
// `--expand` starts it; otherwise a prior crawl resumes only when resume-on-start is set.
func shouldResume(s store.Backend, autostart bool, stdout, stderr io.Writer) bool {
	if autostart {
		return true
	}
	running, known, err := s.DiscoveryIntent()
	if err != nil {
		fmt.Fprintf(stderr, "serve: cannot read whether discovery should be running: %v\n", err)
		return false
	}
	if !known || !running {
		return false
	}

	resume, err := s.DiscoveryResumeOnStart()
	if err != nil {
		fmt.Fprintf(stderr, "serve: cannot read whether discovery may resume: %v\n", err)
		return false
	}
	if !resume {
		fmt.Fprintln(stdout,
			"serve: discovery was running when this last stopped and is left paused; "+
				"start it from the header, or turn on resume-on-start in Settings")
		return false
	}
	fmt.Fprintln(stdout, "serve: resuming discovery, which was running and is set to resume")
	return true
}

// seedDeclaration applies an untracked local declaration only when the database holds none.
func seedDeclaration(s store.Backend, path string, stderr io.Writer) error {
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

// webhookSecretEnv supplies the secret when no file does; never a flag, since make status prints argv.
const webhookSecretEnv = "ABOM_WEBHOOK_SECRET"

// eventRetryEvery is how often deliveries waiting on an analysis are looked at again.
const eventRetryEvery = 30 * time.Second

func webhookSecret(path string) ([]byte, error) {
	if path == "" {
		return []byte(os.Getenv(webhookSecretEnv)), nil
	}
	secret, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("serve: reading the webhook secret: %w", err)
	}
	return bytes.TrimRight(secret, "\r\n"), nil
}

// receiveEvents turns on the receiver and its processor when a secret is set; the sweep is untouched.
func receiveEvents(
	ctx context.Context, srv *serve.Server, s store.Backend, loop *supervisor.Supervisor,
	reader *catalog.Client, secret []byte, installations func(), codeScanningWait time.Duration,
	stdout io.Writer,
) {
	if len(secret) == 0 {
		return
	}
	wake := make(chan struct{}, 1)
	srv.Events = &serve.EventReceiver{Secret: secret, Arrived: nudge(wake)}
	go newProcessor(s, loop, reader, installations, codeScanningWait, stdout).Run(ctx, wake, eventRetryEvery)
	fmt.Fprintln(stdout, "serve: receiving signed GitHub webhook deliveries at POST /api/events/github")
}

// newProcessor hands the processor only the asks this loop can answer, and reads GitHub through
// the event follow-up reader.
func newProcessor(
	s store.Backend, loop *supervisor.Supervisor, reader *catalog.Client, installations func(),
	codeScanningWait time.Duration, stdout io.Writer,
) *events.Processor {
	processor := &events.Processor{
		Store: s, Queue: loop.Queue, Log: stdout, Installations: installations, Wake: loop.Wake,
		Pace: processPools.Pool(work.PoolEvents, work.UnitGit),
	}
	// Waiting for code scanning buys something only where the crawl takes what it supplies.
	if loop.Expander != nil && loop.Expander.CodeScanning != nil {
		processor.CodeScanningWait = codeScanningWait
	}
	if reader != nil {
		processor.Branches, processor.Databases = reader.ProtectedBranches, reader.FindDatabase
		processor.Setup = setup.Reader{Store: s, Catalog: reader}.Read
		processor.Dependencies = reader.CompareDependencies
	}
	if loop.Enumerate != nil {
		processor.List, processor.ListFully, processor.Read = loop.ListSoon, loop.ListFullySoon, loop.ReadSoon
	}
	if loop.Advisor != nil {
		processor.Advisories = loop.RefreshAdvisoriesSoon
	}
	if loop.Rechecker != nil {
		processor.Probe = loop.Rechecker.Probe
	}
	return processor
}

// nudge wakes whoever reads ch without blocking; one pending wake is as good as several.
func nudge(ch chan<- struct{}) func() {
	return func() {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

type serveConfig struct {
	dbPath        string
	readReplica   string
	replicaMaxLag time.Duration
	readerOnly    bool
	// graphviz is the version every figure is drawn with, read once at startup.
	graphviz       string
	declareFile    string
	addr           string
	uiDir          string
	allowRemote    bool
	diagnostics    bool
	apiURL         string
	enumerateEvery time.Duration
	listEvery      time.Duration
	route          string
	groupProperty  string
	submitTargets  string
	enableTargets  string
	enableAccounts string
	webhookSecret  []byte
	appDir         string
	appPublicReads string
	eventsRelay    string
	// tokens is the App's token source once loaded, nil while reading as the operator.
	tokens    ghapp.TokenSource
	discovery discoveryOptions
	authoring authoringOptions
	// toolchain is what sessions need, nil where nothing checks it.
	toolchain *toolchain.Toolchain
}

// runServe answers interface queries over HTTP until the context is canceled.
func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) (result error) {
	cfg, err := parseServeArgs(args, stderr)
	if err != nil {
		return err
	}
	// Every details page draws with Graphviz, so without it this is a broken installation. Asked
	// apart from ctx, which a caller may cancel before serving to run only the open-time work.
	graphviz, err := layout.Require(context.WithoutCancel(ctx))
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	fmt.Fprintf(stdout, "serve: drawing with Graphviz %s\n", graphviz)
	cfg.graphviz = graphviz
	if cfg.readerOnly {
		return runReader(ctx, cfg, stdout, stderr)
	}
	if reason := volatileDatabase(cfg.dbPath); reason != "" {
		fmt.Fprintf(stderr, "serve: %s\n", reason)
	}

	s, err := openServeStore(cfg, stdout, stderr)
	if err != nil {
		return err
	}
	defer s.Close()

	ctx, s, release, err := coordination.Hold(ctx, s)
	if err != nil {
		return fmt.Errorf("serve: acquiring coordinator ownership: %w", err)
	}
	defer func() { result = errors.Join(result, release()) }()

	app, err := readerApp(cfg.appDir, cfg.appPublicReads, cfg.apiURL)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	tokens := readerTokens(app)
	cfg.tokens = tokens
	if cfg.webhookSecret, err = eventSecret(app, cfg.appDir, cfg.webhookSecret); err != nil {
		return err
	}

	cfg.toolchain = newToolchain(s, &cfg, stderr)
	openCustomQueries(cfg.discovery.customQLDir, cfg.discovery.codeqlPath, stdout, stderr)

	loop, err := newSupervisor(s, cfg.discovery, tokens, stderr)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	wireTrees(ctx, loop, cfg.apiURL, tokens)
	wireCodeScanning(ctx, loop, s, cfg)
	primeServedStore(s, loop.Queue, stdout, stderr)

	srv, err := newConfiguredServer(ctx, cfg, s, loop, tokens, stderr)
	if err != nil {
		return err
	}
	if cfg.readReplica != "" {
		reader, err := openReadReplica(cfg, s)
		if err != nil {
			return err
		}
		defer reader.Close()
		srv.SetReplica(reader, cfg.replicaMaxLag)
	}
	resync := make(chan struct{}, 1)
	receiveEvents(ctx, srv, s, loop, eventsClient(ctx, cfg.apiURL, s, tokens, processPools),
		cfg.webhookSecret, nudge(resync), cfg.discovery.codeScanningWait, stdout)
	if err := startRelay(ctx, cfg, stdout); err != nil {
		return err
	}
	if app != nil {
		fmt.Fprintln(stdout, describeApp(app))
		go syncInstallations(ctx, app, s, installationSyncEvery, resync, stderr)
	}
	writeIdentityBanner(stdout, s)

	go srv.CheckStaleness()
	go srv.ReadUnreadDeclarations(ctx)
	go srv.MeterPools(ctx, poolFlushEvery)
	go pruneHistory(ctx, s, time.Hour, time.Now, stdout)
	pollExternalReferences(ctx, cfg, s, loop, stdout)

	loopCtx, stopLoop := context.WithCancel(ctx)
	defer stopLoop()
	go func() {
		if err := loop.Run(loopCtx); err != nil {
			fmt.Fprintf(stderr, "serve: discovery stopped: %v\n", err)
		}
	}()
	if shouldResume(s, cfg.discovery.autostart, stdout, stderr) {
		if problem := loop.Start(); problem != nil {
			fmt.Fprintf(stderr, "serve: discovery cannot start: %s\n  %s\n",
				problem.Reason, problem.Remedy)
		} else {
			fmt.Fprintln(stdout, "serve: discovery running")
		}
	}

	return serveHTTP(ctx, &http.Server{Addr: cfg.addr, Handler: srv.Handler()}, cfg.addr, stdout)
}

func parseServeArgs(args []string, stderr io.Writer) (serveConfig, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := databaseFlag(fs)
	readReplica := fs.String("read-replica", os.Getenv("ABOM_READ_REPLICA"),
		"PostgreSQL standby URL for pure API reads (defaults to ABOM_READ_REPLICA)")
	replicaMaxLag := fs.Duration("replica-max-lag", 0,
		"maximum measured standby lag for ordinary reads; zero requires catch-up, writes read back from primary")
	readerOnly := fs.Bool("reader-only", false, "serve read-only APIs without acquiring a coordinator lease or starting work")
	declareFile := fs.String("declare-file", declare.DefaultFileName,
		"local file declaring owned accounts, applied only when nothing is declared")
	addr := fs.String("addr", "localhost:8080", "address to listen on")
	uiDir := fs.String("ui-dir", "", "directory containing the published interface (optional)")
	allowRemote := fs.Bool("allow-remote-control", false,
		"allow non-loopback request hosts for reads and controls; this does not authenticate callers")
	diagnostics := fs.Bool("diagnostics", false,
		"expose /api/metrics and /debug/pprof/ for local performance analysis")
	apiURL := fs.String("api-url", defaultAPIURL(), "GitHub API base URL")
	enumerateEvery := fs.Duration("enumerate-every", supervisor.DefaultEnumerateEvery,
		"how often the declared accounts are re-read for new repositories (0: never)")
	listEvery := fs.Duration("list-every", enumerate.DefaultFullListingEvery,
		"how often every page of an account is read, which is the only way a deletion is seen")
	route := fs.String("read-route", "",
		"which API reads an account: graphql, rest, or empty to prefer graphql and fall back")
	groupProperty := fs.String("group-property", "",
		"repository custom property to group by on screen, for example business_id")
	submitTargets := fs.String("submit-targets", "",
		"comma-separated repositories that may receive dependency snapshots")
	enableTargets := fs.String("enable-targets", "",
		"comma-separated repositories whose default setup may gain actions, once an approved preview lists them")
	enableAccounts := fs.String("enable-accounts", "",
		"comma-separated accounts whose declared repositories' default setup may gain actions, once approved")
	webhookSecretFile := fs.String("webhook-secret-file", "",
		"file holding the secret GitHub signs webhook deliveries with "+
			"(default: $"+webhookSecretEnv+"; neither refuses every delivery; an App's own is used when one is configured)")
	appDir := fs.String("app-dir", "",
		"directory holding the GitHub App ABOM reads as (default: github-app beside --db; none there reads as you)")
	eventsRelay := fs.String("events-relay", "",
		"relay channel to read deliveries from, such as https://smee.io/<channel> (default: $"+eventsRelayEnv+")")
	appPublicReads := fs.String("app-public-reads", "",
		"account whose installation reads owners the App is not installed on (default: the earliest installation)")
	var d discoveryOptions
	fs.BoolVar(&d.autostart, "expand", false, "start discovery as soon as the server is up")
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
	fs.IntVar(&d.rateLimitPerHour, "rate-limit-per-hour", 0,
		"git requests an hour for fetching and probing, where Settings saved no rate (0: none)")
	fs.StringVar(&d.cacheDir, "cache-dir", "",
		"retain fetched checkouts here and reuse each commit across references (default: off)")
	fs.Int64Var(&d.cacheMaxMB, "cache-max-mb", defaultCacheMaxMB,
		"ceiling for retained checkouts, least recently used evicted first (0: unbounded)")
	fs.StringVar(&d.databaseCacheDir, "database-cache-dir", "",
		"retain built CodeQL databases here and analyze them again without extracting (default: off)")
	fs.Int64Var(&d.databaseCacheMaxMB, "database-cache-max-mb", defaultDatabaseCacheMaxMB,
		"ceiling for retained databases, least recently used evicted first (0: unbounded)")
	fs.StringVar(&d.customQLDir, "custom-ql-dir", "",
		"directory holding your own CodeQL pack, scaffolded if empty (default: off)")
	fs.IntVar(&d.aggregateAfter, "aggregate-after-edges", supervisor.DefaultAfterEdges,
		"dependencies that must arrive before reach is recomputed")
	fs.DurationVar(&d.aggregateMaxAge, "aggregate-max-age", supervisor.DefaultMaxAge,
		"longest reach may go unmeasured, however little arrives")
	fs.DurationVar(&d.aggregateMinPeriod, "aggregate-min-interval", supervisor.DefaultMinInterval,
		"shortest gap between two reach rebuilds")
	fs.BoolVar(&d.codeScanningReuse, "code-scanning-reuse", true,
		"take code scanning's database and default setup's analysis where each is what ABOM would "+
			"produce; false builds and evaluates everything")
	fs.DurationVar(&d.codeScanningWait, "code-scanning-wait", 10*time.Minute,
		"longest a push to an owned default branch whose default setup reads Actions waits for code "+
			"scanning's own analysis of it before ABOM builds anyway (0: never waits)")
	var authoring authoringOptions
	authoring.register(fs)
	if err := fs.Parse(args); err != nil {
		return serveConfig{}, err
	}
	if *dbPath == "" {
		fs.Usage()
		return serveConfig{}, errors.New("serve: --db is required")
	}
	if *replicaMaxLag < 0 {
		return serveConfig{}, errors.New("replica-max-lag must not be negative")
	}
	if *readerOnly && d.autostart {
		return serveConfig{}, errors.New("a reader-only instance cannot start discovery")
	}
	if *readReplica != "" && (!store.IsPostgresTarget(*dbPath) || !store.IsPostgresTarget(*readReplica)) {
		return serveConfig{}, errors.New("read replicas require PostgreSQL primary and standby URLs")
	}
	secret, err := webhookSecret(*webhookSecretFile)
	if err != nil {
		return serveConfig{}, err
	}
	return serveConfig{
		dbPath:         *dbPath,
		readReplica:    *readReplica,
		replicaMaxLag:  *replicaMaxLag,
		readerOnly:     *readerOnly,
		declareFile:    *declareFile,
		addr:           *addr,
		uiDir:          *uiDir,
		allowRemote:    *allowRemote,
		diagnostics:    *diagnostics,
		apiURL:         *apiURL,
		enumerateEvery: *enumerateEvery,
		listEvery:      *listEvery,
		route:          *route,
		groupProperty:  *groupProperty,
		submitTargets:  *submitTargets,
		enableTargets:  *enableTargets,
		enableAccounts: *enableAccounts,
		webhookSecret:  secret,
		appDir:         cmp.Or(*appDir, ghapp.DefaultDir(*dbPath)),
		appPublicReads: *appPublicReads,
		eventsRelay:    cmp.Or(*eventsRelay, os.Getenv(eventsRelayEnv)),
		discovery:      d,
		authoring:      authoring,
	}, nil
}

func openServeStore(cfg serveConfig, stdout, stderr io.Writer) (store.Backend, error) {
	s, err := store.OpenBackend(cfg.dbPath, store.Options{})
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", store.DatabaseLabel(cfg.dbPath), err)
	}
	if err := s.Migrate(); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("migrating %s: %w", store.DatabaseLabel(cfg.dbPath), err)
	}
	migratePolicyScopes(s, cfg.groupProperty, stdout, stderr)
	if err := seedDeclaration(s, cfg.declareFile, stderr); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func primeServedStore(s store.Backend, queue *frontier.Queue, stdout, stderr io.Writer) {
	if _, err := s.LinkFindingsToNodes(); err != nil {
		fmt.Fprintf(stderr, "serve: attributing findings: %v\n", err)
	}
	if _, err := s.ClassifyEdgeBoundaries(); err != nil {
		fmt.Fprintf(stderr, "serve: classifying edge boundaries: %v\n", err)
	}
	prepareQueue(s, queue, stdout, stderr)
	reclaimAbandonedWork(s, queue, stdout, stderr)
}

func newConfiguredServer(
	ctx context.Context, cfg serveConfig, s store.Backend, loop *supervisor.Supervisor,
	tokens ghapp.TokenSource, stderr io.Writer,
) (*serve.Server, error) {
	srv := &serve.Server{
		Store: s, Discovery: loop, AllowRemoteControl: cfg.allowRemote, Log: stderr,
		Graphviz:     cfg.graphviz,
		Diagnostics:  cfg.diagnostics,
		WorkDir:      loop.Options.WorkDir,
		Pools:        processPools,
		RecheckFloor: cmp.Or(loop.RecheckTTL, supervisor.DefaultRecheckTTL),
	}
	srv.Executor = newRunExecutor(s, loop.Expander, loop.Options.WorkDir, stderr)
	if tester, ok := loop.Expander.Resolver.(serve.QueryTester); ok {
		srv.Tester, srv.QueryDir = tester, runner.CustomPackRoot()
	}
	if printer, ok := loop.Expander.Resolver.(serve.ASTPrinter); ok && srv.QueryDir != "" {
		srv.Printer = printer
	}
	if resolver := loop.Expander.Resolver; resolver != nil {
		srv.ReadDeclarations = func(ctx context.Context, unread []store.CatalogQuery) (map[string]string, error) {
			return expand.ReadDeclarations(ctx, s, resolver, unread)
		}
	}
	if sessions, agentFor := newAuthoring(s, cfg.authoring, loop.Expander, loop.Options.WorkDir, stderr); sessions != nil {
		srv.Authoring = sessions
		if cfg.toolchain != nil {
			cfg.toolchain.ApplyCopilot = func(cli string) { sessions.SetAgent(agentFor(cli)) }
		}
	}
	if cfg.toolchain != nil {
		srv.Toolchain = cfg.toolchain
	}
	reader, err := readClient(ctx, cfg.apiURL, s, cfg.route, tokens)
	if err != nil {
		return nil, fmt.Errorf("serve: %w", err)
	}
	srv.Enumerate = reader.Sweep
	srv.EnumerateSchema = reader.PropertySchema
	srv.EnumerateBranches = reader.ProtectedBranches
	srv.EnumerateRepository = reader.ReadRepository
	setupReader := setup.Reader{Store: s, Catalog: reader}
	srv.EnumerateSetup = setupReader.Read
	srv.EnumerateConfigurations = setupReader.ReadConfigurations
	srv.CodeScanningWrites = codeScanningWrites(ctx, cfg, s, setupReader)
	srv.FindDatabase = reader.FindDatabase
	srv.Files = reader
	srv.Comparer = &compare.Comparer{Store: s, Read: reader}
	srv.FullListingEvery = cfg.listEvery
	srv.GroupProperty = cfg.groupProperty
	srv.QueueOwned = func() (int, error) { return expand.SeedOwnedRepositories(s, loop.Queue) }
	srv.QueueClosure = loop.Queue.Closure
	verifier := lockVerifier(s, tokens)
	fetchTree, locks := verifier.Fetch, processPools.Pool(work.PoolLocks, work.UnitGit)
	verifier.Fetch = func(ctx context.Context, dest, owner, repo, commit string) error {
		if err := locks.Take(ctx); err != nil {
			return err
		}
		return fetchTree(ctx, dest, owner, repo, commit)
	}
	srv.VerifyLock = func(ctx context.Context, repository, mode string) (store.LockVerification, error) {
		return verifier.Request(ctx, repository, mode, "interface")
	}
	wireEnumeration(loop, srv, s, cfg.enumerateEvery)
	wireSnapshots(srv, s, githubToken(ctx), splitTargets(cfg.submitTargets),
		catalog.New(cfg.apiURL, githubToken(ctx), nil).HeadSHA)
	if cfg.uiDir != "" {
		srv.UI = os.DirFS(cfg.uiDir)
	}
	return srv, nil
}

func serveHTTP(ctx context.Context, httpServer *http.Server, addr string, stdout io.Writer) error {
	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(stdout, "%s serve: listening on http://%s (pid %d)\n",
			time.Now().UTC().Format(time.RFC3339), addr, os.Getpid())
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
