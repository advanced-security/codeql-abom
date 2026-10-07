package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/discover"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// runDiscover records which repositories in an organization have a CodeQL database, without
// downloading anything yet.
func runDiscover(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	org := fs.String("org", "", "GitHub organization to enumerate (required)")
	language := fs.String("language", "actions", "CodeQL language to look for a database in")
	apiURL := fs.String("api-url", defaultAPIURL(), "GitHub API base URL")
	concurrency := fs.Int("concurrency", defaultConcurrency, "repositories probed at once")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" || *org == "" {
		fs.Usage()
		return errors.New("discover: --db and --org are required")
	}

	s, err := store.Open(*dbPath, store.Options{BulkLoad: true})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	tokens, err := dbReaderTokens(*dbPath)
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}
	client := githubClient(ctx, *apiURL, s, tokens)
	d := &discover.Discoverer{
		Source:      client,
		Store:       s,
		Language:    *language,
		Concurrency: *concurrency,
		OnEvent:     logFailures(stdout),
	}

	stats, err := d.Run(ctx, *org)
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}
	fmt.Fprintf(stdout, "discover: %d repositories probed, %d with a %s database, %d failed\n",
		stats.Processed+stats.Failed, stats.Rows, *language, stats.Failed)
	if stats.Skipped > 0 {
		fmt.Fprintf(stdout, "discover: %d unprobed (no workflow files on the default branch); no new database measurement\n",
			stats.Skipped)
	}
	// Stated rather than inferred from a shorter run: an unchanged answer still cost a round trip,
	// and a cache that quietly stopped working looks identical to one that never helped.
	spent := client.Stats()
	fmt.Fprintf(stdout, "discover: %d reads, %d answered unchanged\n",
		spent.Requests, spent.NotModified)
	if budget := spent.GraphQL; budget.Known {
		fmt.Fprintf(stdout, "discover: %d GraphQL points spent, %d of %d GraphQL points left\n",
			spent.Points, budget.Remaining, budget.Limit)
	}
	if b := spent.RateLimit; b.Known {
		fmt.Fprintf(stdout, "discover: %d of %d requests left, until %s\n",
			b.Remaining, b.Limit, b.ResetAt.UTC().Format(time.RFC3339))
	} else {
		fmt.Fprintln(stdout, "discover: no REST budget was reported, so none is known")
	}
	// Reported rather than absorbed: a crawl that was held back and one that was not are different
	// evidence about whether the pace is set right.
	if spent.Waits > 0 {
		fmt.Fprintf(stdout, "discover: paced %d times, %s waiting\n",
			spent.Waits, spent.Waited.Round(time.Millisecond))
	}
	return nil
}

// defaultConcurrency bounds simultaneous GitHub API and CodeQL work. Small enough that a
// laptop-class runner is not overwhelmed, large enough that a single slow repository does not
// serialize an entire organization behind it.
const defaultConcurrency = 4

// defaultAPIURL prefers the URL GitHub Actions injects, which is what makes this work
// unchanged against GitHub Enterprise Server.
func defaultAPIURL() string {
	if v := os.Getenv("GITHUB_API_URL"); v != "" {
		return v
	}
	return "https://api.github.com"
}

// githubToken reads the token GitHub Actions provides automatically, then the name the gh CLI
// honors, and finally asks the CLI itself.
//
// The last step lets a local run use the operator's gh credential instead of the anonymous API
// budget, which can silently miss private repositories.
//
// It returns "" when there is no credential anywhere, which reads whatever is public. That is a
// worse answer than an error and callers should say so.
func githubToken(ctx context.Context) string {
	if v := os.Getenv("GITHUB_TOKEN"); v != "" {
		return v
	}
	if v := os.Getenv("GH_TOKEN"); v != "" {
		return v
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return ""
	}
	// Bounded because this runs during startup, and a gh that never answers would otherwise
	// hang the server before it listens.
	ctx, cancel := context.WithTimeout(ctx, tokenTimeout)
	defer cancel()
	token, err := ghauth.Token(ctx, ghauth.Exec)
	if err != nil {
		return ""
	}
	return token
}

// githubPace spaces every REST read this process makes.
//
// One limiter rather than one per client: the budget belongs to the token, so two clients pacing
// independently would each think it had the whole of it and spend twice the rate.
var githubPace = work.NewLimiter(0)

// githubClient reads GitHub on this database's behalf, remembering validators in it so that asking
// again about something unchanged costs a round trip and no primary quota.
func githubClient(ctx context.Context, apiURL string, s *store.Store, tokens ghapp.TokenSource) *catalog.Client {
	c := newReader(ctx, apiURL, tokens)
	c.Cache = s.ResponseCache()
	c.Pace = githubPace
	c.REST = processPools.Pool(work.PoolEnumeration, work.UnitREST)
	c.GraphQL = processPools.Pool(work.PoolEnumeration, work.UnitGraphQL)
	return c
}

// eventsClient reads GitHub for the event processor. Following up an event is work of its own, so
// what it reads — all of it REST — is charged to the event follow-up pool, not enumeration's.
func eventsClient(ctx context.Context, apiURL string, s *store.Store, tokens ghapp.TokenSource,
	pools *work.TokenPools) *catalog.Client {
	c := newReader(ctx, apiURL, tokens)
	c.Cache = s.ResponseCache()
	c.Pace = githubPace
	c.REST = pools.Pool(work.PoolEvents, work.UnitREST)
	return c
}

// readClient is githubClient with the route the operator chose, refusing a name it does not know.
//
// Refused rather than ignored: a misspelled route that silently fell back to the default would put
// the reader on a route they did not choose while the flag says otherwise.
func readClient(
	ctx context.Context, apiURL string, s *store.Store, route string, tokens ghapp.TokenSource,
) (*catalog.Client, error) {
	switch route {
	case "", catalog.RouteGraphQL, catalog.RouteREST:
	default:
		return nil, fmt.Errorf("read route %q is not %s, %s, or empty",
			route, catalog.RouteGraphQL, catalog.RouteREST)
	}
	c := githubClient(ctx, apiURL, s, tokens)
	c.Route = route
	return c, nil
}

// tokenTimeout bounds the gh call that reads the stored credential.
const tokenTimeout = 10 * time.Second

// logFailures reports failed jobs as they happen; a run over thousands of repositories that
// only summarizes at the end would surface a repeated failure as a single confusing number.
func logFailures(w io.Writer) func(work.Event) {
	return func(ev work.Event) {
		if ev.Kind == work.EventFailed {
			fmt.Fprintf(w, "  %s: %v\n", ev.Job.Repository, ev.Err)
		}
	}
}
