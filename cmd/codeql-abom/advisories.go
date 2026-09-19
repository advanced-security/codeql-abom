package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/advanced-security/codeql-abom/internal/advisory"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// advisoryRateLimitPerHour paces the advisory endpoint well under the authenticated budget of
// 5000 an hour, because discovery is the crawl's real work and must not be starved of quota by
// enrichment running beside it.
const advisoryRateLimitPerHour = 600

// fetcher is the shape of advisory.Client.Fetch, named so the refresh can be tested without a
// server standing in for GitHub.
type fetcher func(ctx context.Context, packages []string) ([]advisory.Advisory, error)

// newAdvisor returns the refresh the supervisor runs after a cycle that discovered something.
//
// Unauthenticated it would be 60 requests an hour, which an organization-sized package list
// exhausts in one pass, so a gh login is used when the environment provides no token.
func newAdvisor(s *store.Store, logTo io.Writer) func(context.Context) (int, error) {
	client := &advisory.Client{
		// Background because this is startup wiring, not a request: the credential is read
		// once, before there is a cycle to cancel.
		Token: githubToken(context.Background()),
		Pace:  work.NewLimiter(advisoryRateLimitPerHour),
	}
	return advisorFor(s, client.Fetch, logTo)
}

// advisorFor refreshes the advisories for whatever packages the graph currently names.
//
// The package list is re-read every time rather than captured, because the point of running
// this on a cycle that ingested is that the set of packages has just changed.
func advisorFor(s *store.Store, fetch fetcher, logTo io.Writer) func(context.Context) (int, error) {
	return func(ctx context.Context) (int, error) {
		packages, err := s.AdvisoryPackages()
		if err != nil {
			return 0, fmt.Errorf("listing the packages the graph names: %w", err)
		}
		if len(packages) == 0 {
			return 0, nil
		}

		advisories, err := fetch(ctx, packages)
		if err != nil {
			return 0, fmt.Errorf("fetching advisories for %d packages: %w", len(packages), err)
		}

		n, err := s.AddAdvisories(ingest.Advisories(advisories))
		if err != nil {
			return 0, fmt.Errorf("recording %d advisories: %w", len(advisories), err)
		}
		if logTo != nil {
			fmt.Fprintf(logTo, "%s discovery: checked %d packages against %d advisories\n",
				time.Now().UTC().Format(time.RFC3339), len(packages), n)
		}
		return n, nil
	}
}

// advisoryToken prefers the environment, then a gh login.
//
// serve is normally run by hand on a workstation where gh is already authenticated and no token is
// exported, so reading only the environment would silently leave every refresh on the
// 60-an-hour unauthenticated budget.
