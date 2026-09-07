package main

import (
	"context"
	"fmt"
	"io"

	"github.com/advanced-security/codeql-abom/internal/expand"
)

// requeueSelection puts back only the work that was actually asked for, before the round loop
// claims anything.
//
// A whole-tier requeue re-clones every repository in the tier. The skip makes the analysis free
// when nothing changed, but the checkout in front of it is not free, so the selection is what
// makes re-processing proportional to what moved.
//
// The selecting lives in the expansion so this and the Discovery page cannot drift into two
// different answers to "what is out of date".
func requeueSelection(
	ctx context.Context, e *expand.Expander, stale bool, repository string, stdout io.Writer,
) error {
	if stale {
		got, err := e.RequeueStale(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout,
			"expand-all: %d dependencies requeued as out of date against CodeQL %s and %d packs\n",
			got.Requeued, got.CLIVersion, got.Packs)
	}

	if repository == "" {
		return nil
	}
	got, err := e.RequeueRepository(repository)
	if err != nil {
		return err
	}
	switch {
	case got.Requeued > 0:
		fmt.Fprintf(stdout,
			"expand-all: %d dependencies requeued for %s, %d ahead of the ranking\n",
			got.Requeued, repository, got.Expedited)
	case got.Expedited > 0:
		fmt.Fprintf(stdout, "expand-all: %s was already queued, %d moved ahead of the ranking\n",
			repository, got.Expedited)
	default:
		// Saying so matters: the alternative is an operator watching a crawl that was never
		// going to reach the thing they asked about.
		fmt.Fprintf(stdout, "expand-all: nothing analyzed for %s to requeue\n", repository)
	}
	return nil
}
