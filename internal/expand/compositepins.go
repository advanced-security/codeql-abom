package expand

import (
	"fmt"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// ExpediteCompositePins prioritizes the composite actions the operator's workflows pin, at the
// references they pin, so the crawl reads the trees a submission is actually made of.
//
// It reprioritizes and never enqueues. SeedFrontier already queues every (node, ref) an owned
// workflow names, and on the reference corpus all 35 unanalyzed composite pins already had pending
// rows sitting at claim positions 50 to 145 of 437 — not starved, just not yet reached. An earlier
// version of this called Enqueue, added exactly zero rows, and looked like it had worked.
//
// This is the ceiling on transitive discovery: a child edge is attributable to a consumer only
// when the parent was analyzed at the reference that consumer pinned, so until these are read the
// submission reports a supply chain shallower than the one that exists.
func ExpediteCompositePins(s *store.Store, q *frontier.Queue) (int, error) {
	pins, err := s.UnanalyzedCompositePins()
	if err != nil {
		return 0, fmt.Errorf("reading unanalyzed composite pins: %w", err)
	}

	refs := make([]frontier.NodeRef, 0, len(pins))
	for _, p := range pins {
		if p.Owner == "" || p.Repo == "" {
			// Nothing can be checked out for a reference that names no repository.
			continue
		}
		refs = append(refs, frontier.NodeRef{NodeKey: p.NodeKey, Ref: p.Ref})
	}
	return q.ExpediteRefs(refs)
}
