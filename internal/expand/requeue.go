package expand

import (
	"context"
	"fmt"
	"strings"
)

// Selection is what one selective requeue put back, and what it judged by.
//
// The identity travels with the count because the count alone cannot be checked: "1,742
// requeued" is a number, "1,742 out of date against CodeQL 2.26.4 and 4 packs" is a claim
// anybody can test.
type Selection struct {
	Requeued int
	// Expedited counts what will run ahead of the ranking. Requeueing and expediting are
	// separate decisions, so they are separate numbers.
	Expedited int
	// CLIVersion and Packs are empty for a selection that did not judge staleness.
	CLIVersion string
	Packs      int
}

// RequeueStale puts back the dependencies whose repository was not analyzed at the identity the
// bound suites resolve to now.
//
// One implementation for the command line and the interface. Assembled separately they would
// disagree the moment either grew a field, and the disagreement reads as a crawl doing too much
// rather than as a bug.
func (e *Expander) RequeueStale(ctx context.Context) (Selection, error) {
	cliVersion, digests, err := e.CurrentIdentity(ctx)
	if err != nil {
		return Selection{}, fmt.Errorf("resolving what the bound suites run now: %w", err)
	}
	n, err := e.Store.RequeueStale(cliVersion, digests)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Requeued: n, CLIVersion: cliVersion, Packs: len(digests)}, nil
}

// RequeueRepository puts back the dependencies one repository answers, ahead of the ranking.
//
// Expediting is the second half of the same request: someone who has just fixed a dependency
// they own wants that one answer, and filing it behind nine hundred better-scored items refuses
// the request in all but name. It runs even when nothing was put back, because a repository
// already in the queue can still be moved to the front — which is what "run this next" meant.
func (e *Expander) RequeueRepository(name string) (Selection, error) {
	name = strings.TrimSpace(name)
	if e.SourcePolicy != nil {
		e.SourcePolicy.Invalidate(name)
	}
	n, err := e.Store.RequeueRepository(name)
	if err != nil {
		return Selection{}, err
	}

	keys, err := e.Store.FrontierKeysFor(name)
	if err != nil {
		return Selection{}, err
	}
	expedited, err := e.Queue.Expedite(keys...)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Requeued: n, Expedited: expedited}, nil
}
