package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/pipeline"
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
	aggregateAfter     int
	aggregateMaxAge    time.Duration
	aggregateMinPeriod time.Duration
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

// newSupervisor wires the expansion loop serve hosts.
//
// It is built whether or not it will autostart, because the interface can start it later and a
// button that has nothing behind it is worse than no button.
func newSupervisor(s *store.Store, opts discoveryOptions, logTo io.Writer) (*supervisor.Supervisor, error) {
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

	queue := &frontier.Queue{Store: s, MaxDepth: opts.maxDepth}

	var pace *work.Limiter
	if opts.rateLimitPerHour > 0 {
		pace = work.NewLimiter(opts.rateLimitPerHour)
	}

	w := store.NewWriter(s, 0)
	r := &runner.Runner{CodeQLPath: opts.codeqlPath, Threads: opts.codeqlThreads(), RAMMB: opts.ramMB}
	checkout := &expand.Checkout{}

	// Seeded from what is already held so a restart does not re-fetch a set collected minutes ago.
	fetchedAt, _, err := s.AdvisoriesFetchedAt()
	if err != nil {
		return nil, fmt.Errorf("reading how old the advisory set is: %w", err)
	}

	return &supervisor.Supervisor{
		Store: s,
		Queue: queue,
		Expander: &expand.Expander{
			Queue:    queue,
			Store:    s,
			Ingester: ingest.New(s, w),
			Writer:   w,
			Fetcher:  checkout,
			Builder:  r,
			CodeQL:   r,
			Resolver: r,
			Decode:   expand.Decoder(pipeline.DefaultDecoder(opts.codeqlPath)),
			Pace:     pace,
			// Without this a repository that fails every time is invisible: the count moves
			// and nothing says which one, or why.
			OnEvent: logDiscoveryFailures(logTo),
		},
		Rechecker: &expand.Rechecker{Queue: queue, Probe: checkout, Pace: pace},
		Builder:   &graph.Builder{Store: s},
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
		Preflight: preflight(opts.codeqlPath, opts.queryPath, dir),
		Sweeper:   func() (int, error) { return expand.SweepDiscoveries(s, queue) },
		Reclassifier: func() (int, error) {
			return expand.ReclassifySupplyChain(s, queue)
		},
		Advisor: newAdvisor(s, logTo),

		AdvisoryMaxAge:      opts.advisoryMaxAge,
		AdvisoryMinInterval: opts.advisoryMinPeriod,
		LastAdvisoryAt:      fetchedAt,

		Log: logTo,
	}, nil
}

// logDiscoveryFailures names the repository behind each failed analysis.
//
// It carries a timestamp because these land in a server log that runs for hours, unlike
// logFailures' output, which sits under the run that produced it.
func logDiscoveryFailures(w io.Writer) func(work.Event) {
	return func(ev work.Event) {
		if ev.Kind != work.EventFailed || w == nil {
			return
		}
		fmt.Fprintf(w, "%s discovery: FAILED %s: %v\n",
			time.Now().UTC().Format(time.RFC3339), ev.Job.Repository, ev.Err)
	}
}

// preflight reports what would stop discovery from running, with the command that fixes it.
//
// It runs at every start rather than once, because the answer changes: a CLI gets installed, a
// disk fills up, and the operator is told either way instead of watching a loop do nothing.
func preflight(codeqlPath, queryPath, workDir string) func() *supervisor.Problem {
	return func() *supervisor.Problem {
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
		if _, err := os.Stat(queryPath); err != nil {
			return &supervisor.Problem{
				Reason: fmt.Sprintf("the edges query %s cannot be read", queryPath),
				Remedy: "restart serve with --query pointing at AbomEdges.ql",
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
