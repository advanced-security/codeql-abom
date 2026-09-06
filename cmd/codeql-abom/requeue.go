package main

import (
	"context"
	"fmt"
	"io"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// requeueSelection puts back only the work that was actually asked for, before the round loop
// claims anything.
//
// A whole-tier requeue re-clones every repository in the tier. The skip makes the analysis free
// when nothing changed, but the checkout in front of it is not free, so the selection is what
// makes re-processing proportional to what moved.
func requeueSelection(
	ctx context.Context, s *store.Store, queue *frontier.Queue, e *expand.Expander,
	stale bool, repository string, stdout io.Writer,
) error {
	if stale {
		cliVersion, digests, err := e.CurrentIdentity(ctx)
		if err != nil {
			return fmt.Errorf("resolving what the bound suites run now: %w", err)
		}
		n, err := s.RequeueStale(cliVersion, digests)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout,
			"expand-all: %d dependencies requeued as out of date against CodeQL %s and %d packs\n",
			n, cliVersion, len(digests))
	}

	if repository == "" {
		return nil
	}
	n, err := s.RequeueRepository(repository)
	if err != nil {
		return err
	}
	if n == 0 {
		// Saying so matters: the alternative is an operator watching a crawl that was never
		// going to reach the thing they asked about.
		fmt.Fprintf(stdout, "expand-all: nothing analyzed for %s to requeue\n", repository)
		return nil
	}

	keys, err := s.FrontierKeysFor(repository)
	if err != nil {
		return err
	}
	expedited, err := queue.Expedite(keys...)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "expand-all: %d dependencies requeued for %s, %d ahead of the ranking\n",
		n, repository, expedited)
	return nil
}
