package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// discoveryOptions are the knobs serve exposes over the expansion loop.
type discoveryOptions struct {
	autostart          bool
	queryPath          string
	queryID            string
	suite              string
	codeqlPath         string
	workDir            string
	concurrency        int
	threads            int
	ramMB              int
	limit              int
	maxDepth           int
	interval           time.Duration
	lease              time.Duration
	recheckTTL         time.Duration
	advisoryMaxAge     time.Duration
	advisoryMinPeriod  time.Duration
	rateLimitPerHour   int
	cacheDir           string
	cacheMaxMB         int64
	databaseCacheDir   string
	databaseCacheMaxMB int64
	customQLDir        string
	aggregateAfter     int
	aggregateMaxAge    time.Duration
	aggregateMinPeriod time.Duration
	codeScanningReuse  bool
	codeScanningWait   time.Duration
}

// codeqlThreads divides the machine between concurrent analyses instead of letting each claim
// all of it.
//
// CodeQL defaults to every core, so four concurrent database builds oversubscribe a machine
// fourfold and the contention costs more than the parallelism wins. Zero keeps CodeQL's own
// default, for anyone who would rather tune it themselves.
func (o discoveryOptions) codeqlThreads() int {
	if o.threads != 0 {
		return o.threads
	}
	if o.concurrency <= 1 {
		return 0
	}
	return max(1, runtime.NumCPU()/o.concurrency)
}

// newRunExecutor carries out recorded runs.
//
// Built from the same *runner.Runner the supervisor holds, deliberately: that is what shares the
// CodeQL lock. Two runners would each hold their own, and a run would contend with the crawl for
// one local compilation cache while both believed they had it.
func newRunExecutor(
	s store.Backend, x *expand.Expander, workDir string, logTo io.Writer,
) *expand.Executor {
	return &expand.Executor{
		Store:    s,
		Resolver: x.Resolver,
		Analyzer: x.Analyzer,
		Ingester: x.Ingester,
		Crawl:    x,
		Suites:   runnableSuites(),
		WorkDir:  filepath.Join(workDir, "runs"),
		Install:  x.Install,
		OnEvent: func(repository, stage string, err error) {
			if err != nil {
				fmt.Fprintf(logTo, "run: %s %s: %v\n", repository, stage, err)
			}
		},
	}
}

// runnableSuites is where a run resolves a query id to a path.
//
// The operator's own suite joins the shipped ones only when a directory is configured, because a
// suite naming a pack that is not on the search path is a resolution failure on every run rather
// than on the one that wanted it. This decides what a run *can name*, never what the crawl runs:
// binding a suite to a tier stays a separate, deliberate act.
func runnableSuites() []string {
	suites := append([]string{}, store.DefaultTierSuites[store.TierDirect]...)
	if runner.CustomPackRoot() != "" {
		suites = append(suites, customql.Suite)
	}
	return suites
}

// crawlQueue is the discovery queue a crawl claims from, kept inside the owned closure.
func crawlQueue(s store.Backend, maxDepth int) *frontier.Queue {
	return &frontier.Queue{Store: s, MaxDepth: maxDepth, Closure: graph.OwnedClosureNames(s)}
}

// newSupervisor wires the expansion loop serve hosts.
//
// It is built whether or not it will autostart, because the interface can start it later and a
// button that has nothing behind it is worse than no button.
func newSupervisor(
	s store.Backend, opts discoveryOptions, tokens ghapp.TokenSource, logTo io.Writer,
) (*supervisor.Supervisor, error) {
	dir := opts.workDir
	if dir == "" {
		tmp, err := os.MkdirTemp("", "abom-serve-*")
		if err != nil {
			return nil, fmt.Errorf("creating a scratch directory: %w", err)
		}
		dir = tmp
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}

	queue := crawlQueue(s, opts.maxDepth)
	if err := configurePools(processPools, s, opts.rateLimitPerHour); err != nil {
		return nil, err
	}
	fetching := processPools.Pool(work.PoolFetching, work.UnitGit)

	w := s.NewWriter(0)
	// The crawl runs for hours, so both the small commands and the BQRS lane are worth a JVM that
	// stays up. Neither is closed here: the supervisor outlives this function and the CLI exits
	// when the process does.
	r := &runner.Runner{
		CodeQLPath: opts.codeqlPath, Threads: opts.codeqlThreads(), RAMMB: opts.ramMB,
		Lock:   runner.SharedLock(),
		Server: &runner.CLIServer{CodeQLPath: opts.codeqlPath},
		Evaluator: &runner.QueryServer{
			CodeQLPath: opts.codeqlPath, Threads: opts.codeqlThreads(), RAMMB: opts.ramMB,
		},
	}
	checkout := &expand.Checkout{Tokens: tokens}
	cache, err := checkoutCache(opts.cacheDir, opts.cacheMaxMB)
	if err != nil {
		return nil, fmt.Errorf("preparing the checkout cache: %w", err)
	}
	databases, err := databaseCache(opts.databaseCacheDir, opts.databaseCacheMaxMB)
	if err != nil {
		return nil, fmt.Errorf("preparing the database cache: %w", err)
	}

	// Seeded from what is already held so a restart does not re-fetch a set collected minutes ago.
	fetchedAt, _, err := s.AdvisoriesFetchedAt()
	if err != nil {
		return nil, fmt.Errorf("reading how old the advisory set is: %w", err)
	}

	return &supervisor.Supervisor{
		Store: s,
		Queue: queue,
		Expander: &expand.Expander{
			Queue:     queue,
			Store:     s,
			Ingester:  ingest.New(s, w),
			Writer:    w,
			Fetcher:   checkout,
			Probe:     pacedProbe{RefProbe: checkout, pool: fetching},
			Builder:   r,
			CodeQL:    r,
			Resolver:  r,
			Analyzer:  r,
			Decode:    expand.Decoder(r.Decode),
			Pace:      fetching,
			Capacity:  processPools.Pool(work.PoolAnalysis, work.UnitWorkerSeconds),
			Cache:     cache,
			Databases: databases,
			Install:   r.InstallPack,
			// Without this a repository that fails every time is invisible: the count moves
			// and nothing says which one, or why.
			OnEvent: logDiscoveryFailures(logTo),
		},
		Rechecker: &expand.Rechecker{
			Queue: queue, Probe: checkout, Pace: processPools.Pool(work.PoolProbing, work.UnitGit),
		},
		Builder: &graph.Builder{Store: s},
		Options: expand.Options{
			QueryPath:   opts.queryPath,
			QueryID:     opts.queryID,
			Suite:       opts.suite,
			WorkDir:     dir,
			Concurrency: opts.concurrency,
			Limit:       opts.limit,
		},
		Interval:   opts.interval,
		Lease:      opts.lease,
		RecheckTTL: opts.recheckTTL,
		Aggregates: supervisor.AggregatePolicy{
			AfterEdges:  opts.aggregateAfter,
			MaxAge:      opts.aggregateMaxAge,
			MinInterval: opts.aggregateMinPeriod,
		},
		Preflight: preflight(s, opts.codeqlPath, opts.queryPath, dir),
		Sweeper:   func() (int, error) { return expand.SweepDiscoveries(s, queue) },
		Reclassifier: func() (int, error) {
			return expand.ReclassifySupplyChain(s, queue)
		},
		Advisor: newAdvisor(s, tokens, logTo),

		AdvisoryMaxAge:      opts.advisoryMaxAge,
		AdvisoryMinInterval: opts.advisoryMinPeriod,
		LastAdvisoryAt:      fetchedAt,

		Log: logTo,
	}, nil
}

// processPools are the token pools every kind of work this process does draws on. One set per
// process, because GitHub's allowance belongs to the token and two sets would each think they had
// all of it.
var processPools = work.NewTokenPools()

// configurePools applies the rates the operator saved, and the flag's rate to fetching and probing
// where none was saved. A pool with neither has no rate, which leaves its work alone.
func configurePools(pools *work.TokenPools, s store.Backend, gitPerHour int) error {
	rates, err := s.PoolRates()
	if err != nil {
		return fmt.Errorf("reading the pools' rates: %w", err)
	}
	saved := map[string]bool{}
	for _, r := range rates {
		pools.Pool(r.Pool, r.Unit).Configure(r.PerHour, r.Burst)
		saved[r.Pool] = saved[r.Pool] || r.Unit == work.UnitGit
	}
	for _, pool := range []string{work.PoolFetching, work.PoolProbing} {
		if gitPerHour > 0 && !saved[pool] {
			pools.Pool(pool, work.UnitGit).Configure(gitPerHour, 1)
		}
	}
	// One write a minute until the operator says otherwise: each starts a setup run on their runners.
	if !slices.ContainsFunc(rates, func(r store.PoolRate) bool { return r.Pool == work.PoolDefaultSetup }) {
		pools.Pool(work.PoolDefaultSetup, work.UnitREST).Configure(defaultSetupWritesPerHour, 1)
	}
	return nil
}

// defaultSetupWritesPerHour is the default setup pool's rate where the operator saved none.
const defaultSetupWritesPerHour = 60

// pacedProbe draws a reference lookup from the fetching pool: it is a git request, like the clone
// it may save.
type pacedProbe struct {
	expand.RefProbe
	pool *work.TokenPool
}

func (p pacedProbe) Resolve(ctx context.Context, owner, repo, ref string) (string, error) {
	if err := p.pool.Take(ctx); err != nil {
		return "", err
	}
	return p.RefProbe.Resolve(ctx, owner, repo, ref)
}

// logDiscoveryFailures names the repository behind each failed analysis.
//
// It carries a timestamp because these land in a server log that runs for hours, unlike
// logFailures' output, which sits under the run that produced it.
//
// A tree read in full that declares nothing is not a failure, whichever way it settles, so it is
// logged as the reading it was.
func logDiscoveryFailures(w io.Writer) func(work.Event) {
	return func(ev work.Event) {
		if ev.Kind != work.EventFailed || w == nil {
			return
		}
		at := time.Now().UTC().Format(time.RFC3339)
		var nothing *expand.NothingToAnalyze
		if errors.As(ev.Err, &nothing) {
			fmt.Fprintf(w, "%s discovery: read %s: %s\n", at, ev.Job.Repository, nothing.Why)
			return
		}
		fmt.Fprintf(w, "%s discovery: FAILED %s: %v\n", at, ev.Job.Repository, ev.Err)
	}
}

// preflight reports what would stop discovery from running, with the command that fixes it.
//
// It runs at every start rather than once, because the answer changes: a CLI gets installed, a
// disk fills up, and the operator is told either way instead of watching a loop do nothing.
func preflight(s store.Backend, codeqlPath, queryPath, workDir string) func() *supervisor.Problem {
	return func() *supervisor.Problem {
		// First, because it is the one thing here the operator configures rather than installs,
		// and because a crawl with no declared scope is what built a corpus of 204 repositories
		// nobody owns.
		declared, err := s.OwnedScopeDeclared()
		if err != nil {
			return &supervisor.Problem{
				Reason: fmt.Sprintf("the declared scope cannot be read: %v", err),
				Remedy: "check the database is readable, then declare an account with `codeql-abom own`",
			}
		}
		if !declared {
			return &supervisor.Problem{
				Reason: "no owned account is declared, so there is nothing in scope to discover",
				Remedy: "declare the accounts you own on the Your Accounts page, " +
					"or run `codeql-abom own --db <db> --org <name>`",
			}
		}

		binary := codeqlPath
		if binary == "" {
			binary = "codeql"
		}
		if _, err := exec.LookPath(binary); err != nil {
			return &supervisor.Problem{
				Reason: fmt.Sprintf("the CodeQL CLI (%s) is not available", binary),
				Remedy: "install the CodeQL CLI, or restart serve with --codeql pointing at it",
			}
		}
		if _, err := exec.LookPath("git"); err != nil {
			return &supervisor.Problem{
				Reason: "git is not available",
				Remedy: "install git; discovery clones each dependency to analyze it",
			}
		}
		// Only when a single query overrides the stored binding; a suite path is resolved by the
		// CodeQL CLI rather than opened here.
		if queryPath != "" {
			if _, err := os.Stat(queryPath); err != nil {
				return &supervisor.Problem{
					Reason: fmt.Sprintf("the edges query %s cannot be read", queryPath),
					Remedy: "restart serve with --query pointing at AbomEdges.ql",
				}
			}
		}
		probe := filepath.Join(workDir, ".abom-writable")
		if err := os.WriteFile(probe, nil, 0o600); err != nil {
			return &supervisor.Problem{
				Reason: fmt.Sprintf("the scratch directory %s is not writable", workDir),
				Remedy: "restart serve with --work-dir pointing somewhere writable",
			}
		}
		if err := os.Remove(probe); err != nil && !errors.Is(err, os.ErrNotExist) {
			return &supervisor.Problem{
				Reason: fmt.Sprintf("the scratch directory %s cannot be cleaned up", workDir),
				Remedy: "restart serve with --work-dir pointing somewhere writable",
			}
		}
		return nil
	}
}
