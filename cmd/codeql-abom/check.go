package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/checks"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// runCheck runs the built checks on private copies of the database, filing results in its own tables
// and drawing only on the checker's own pools.
func runCheck(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	every := fs.Duration("every", 0, "run the checks again at this interval until interrupted; "+
		"0 runs them once")
	external := fs.Bool("external", true, "run the checks that read GitHub, 3 and 4; false leaves them "+
		"off, and the summary says so")
	recent := fs.Int("recent", 50, "how many subjects that changed most recently each sampled check reads first")
	sample := fs.Int("sample", 300, "how many subjects each sampled check draws at random from each stratum; "+
		"zero disagreements in 300 bounds the rate below 1% at 95% confidence")
	apiURL := fs.String("api-url", defaultAPIURL(), "GitHub API base URL")
	appDir := fs.String("app-dir", "", "the GitHub App to read GitHub as (default: github-app beside --db)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("check: --db is required")
	}
	s, err := store.Open(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()

	var github checks.GitHub
	if *external {
		app, err := readerApp(cmp.Or(*appDir, ghapp.DefaultDir(*dbPath)), "", *apiURL)
		if err != nil {
			return fmt.Errorf("check: %w", err)
		}
		github = &lazyGitHub{build: func() *catalog.Client {
			return checkReader(ctx, *apiURL, readerTokens(app), processPools)
		}}
	}
	build := checkBuild()
	for {
		// Read every run, so a rate the operator sets in Settings paces the next one.
		if err := configurePools(processPools, s, 0); err != nil {
			return fmt.Errorf("check: %w", err)
		}
		runs, err := (&checks.Checker{Store: s, Build: build, Dir: os.TempDir(), GitHub: github,
			Recent: *recent, Sample: *sample,
			Capacity: processPools.Pool(work.PoolConsistency, work.UnitWorkerSeconds),
			GraphQL:  processPools.Pool(work.PoolConsistency, work.UnitGraphQL)}).Run(ctx)
		printChecks(stdout, build, runs, *external)
		if err := serve.RecordPoolSpend(s, processPools); err != nil {
			fmt.Fprintf(stderr, "check: recording what the checker spent: %v\n", err)
		}
		if err != nil {
			return fmt.Errorf("check: %w", err)
		}
		if *every <= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(*every):
		}
	}
}

// checkReader reads GitHub for the checker, charging every request to the checker's own pools. It
// keeps no response cache, which would be a table outside the checker's own.
func checkReader(ctx context.Context, apiURL string, tokens ghapp.TokenSource, pools *work.TokenPools) *catalog.Client {
	c := newReader(ctx, apiURL, tokens)
	c.Pace = githubPace
	c.REST = pools.Pool(work.PoolConsistency, work.UnitREST)
	c.GraphQL = pools.Pool(work.PoolConsistency, work.UnitGraphQL)
	return c
}

// lazyGitHub builds the checker's client on its first question, so a run with nothing to sample asks
// for no credential at all.
type lazyGitHub struct {
	once   sync.Once
	build  func() *catalog.Client
	client *catalog.Client
}

func (l *lazyGitHub) get() *catalog.Client {
	l.once.Do(func() { l.client = l.build() })
	return l.client
}

func (l *lazyGitHub) ResolveRefs(ctx context.Context, qs []catalog.RefQuestion) (catalog.RefBatch, error) {
	return l.get().ResolveRefs(ctx, qs)
}

func (l *lazyGitHub) ProtectedBranches(ctx context.Context, repository string) ([]string, error) {
	return l.get().ProtectedBranches(ctx, repository)
}

// checkBuild includes the reach walk's version, which a change to the walk bumps whatever the revision.
func checkBuild() string {
	info, ok := debug.ReadBuildInfo()
	id := identityFrom(info, ok, version, codeqlVersion)
	name := id.Version
	if id.Development {
		name = "development"
	}
	revision := id.Revision
	if revision == "" {
		revision = "unrecorded"
	}
	if id.Dirty != nil && *id.Dirty {
		revision += "+modified"
	}
	return name + " " + revision + " " + graph.ReachMarkVersion
}

func printChecks(w io.Writer, build string, runs []store.CheckRun, external bool) {
	fmt.Fprintf(w, "build %s\n", build)
	for _, run := range runs {
		fmt.Fprintf(w, "check %d (%.1fs)\n", run.Check, float64(run.WallMillis)/1000)
		for _, p := range run.Parts {
			skipped := 0
			for _, k := range p.Skipped {
				skipped += k.Count
			}
			disagreed := "unread"
			if p.Disagreed != nil {
				disagreed = fmt.Sprint(*p.Disagreed)
			}
			compared := fmt.Sprint(p.Compared)
			if p.Population != nil {
				compared += fmt.Sprintf(" of %d", *p.Population)
			}
			bound := ""
			if p.Bound != nil {
				bound = fmt.Sprintf(", at most %.2f%% disagree at 95%% confidence", *p.Bound*100)
			}
			fmt.Fprintf(w, "  %s: compared %s, disagreed %s, skipped %d%s\n", p.Part, compared,
				disagreed, skipped, bound)
			for _, k := range p.Skipped {
				fmt.Fprintf(w, "    skipped %d: %s\n", k.Count, k.Reason)
			}
		}
	}
	for _, c := range checks.Catalog {
		switch {
		case c.BuiltBy != "":
			fmt.Fprintf(w, "not built: check %d (%s)\n", c.Number, c.BuiltBy)
		case !external && slices.Contains(checks.External, c.Number):
			fmt.Fprintf(w, "off: check %d (it reads GitHub, and --external=false)\n", c.Number)
		}
	}
}
