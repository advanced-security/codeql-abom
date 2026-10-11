package expand

import (
	"strings"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// SeedFrontier populates the frontier from every declared repository's own edges, so a crawl has
// somewhere to start.
//
// enqueueDiscoveries (in expand.go) only ever runs after a repository already in the queue has
// been analyzed; nothing seeds the very first batch. A repository the operator owns needs no such
// precondition; naming a dependency at all is what makes that dependency part of a real supply
// chain, unlike an upstream repository's own CI (see enqueueDiscoveries' doc comment).
//
// Roots come from the declared scope rather than from `repository.origin`; a bootstrap origin is
// not proof that the operator owns the account.
func SeedFrontier(s store.Backend, q *frontier.Queue) (int, error) {
	// The referencing repository is recorded because it is the only answer to "why is this
	// queued at all", and once the item is analyzed the edge that put it there is no longer
	// distinguishable from any other. Several repositories usually name the same dependency;
	// the first by name is chosen so the answer does not change between runs.
	items, err := s.SeedDependencies()
	if err != nil {
		return 0, err
	}
	for i := range items {
		fromKey(&items[i])
		items[i].Depth = 1
	}

	return q.Enqueue(items)
}

// fromKey fills an owned target's owner, repository and path from its key, which its node does
// not carry.
func fromKey(it *frontier.Item) {
	if it.Owner != "" {
		return
	}
	parts := strings.SplitN(it.NodeKey, "/", 3)
	if len(parts) < 2 {
		return
	}
	it.Owner, it.Repo, it.Subpath = parts[0], parts[1], ""
	if len(parts) == 3 {
		it.Subpath = parts[2]
	}
}
