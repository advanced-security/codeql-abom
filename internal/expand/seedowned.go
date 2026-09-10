package expand

import (
	"fmt"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// SeedOwnedRepositories queues the operator's own repositories for analysis.
//
// SeedFrontier reads edges, so it can only queue the dependencies of a repository that has already
// been analyzed — and nothing analyzed the operator's repositories in the first place. On the
// reference corpus that left 198 declared repositories sitting as roots of a graph built entirely
// from other people's code, with an empty database catalog and a queue that had never contained
// one of them.
//
// Analysis batches by `owner/repo@ref` (see groupByRepository), so a repository needs no different
// treatment from a dependency: it is checked out and analyzed by the same loop. Depth 0 is what
// distinguishes it, and claiming is depth-first, so the operator's own work precedes every hop of
// anybody's supply chain.
func SeedOwnedRepositories(s *store.Store, q *frontier.Queue) (int, error) {
	owned, err := s.OwnedRepositories()
	if err != nil {
		return 0, fmt.Errorf("reading owned repositories: %w", err)
	}

	var items []frontier.Item
	for _, fullName := range owned {
		owner, repo, ok := strings.Cut(fullName, "/")
		if !ok || owner == "" || repo == "" {
			// Without both halves there is no checkout to perform, and the row would sit in
			// the queue failing for as long as the queue exists.
			continue
		}
		// DiscoveredFrom is deliberately empty: this is queued because the operator owns it,
		// and naming a referrer would invent a dependency nobody recorded.
		items = append(items, frontier.Item{
			NodeKey: fullName,
			Owner:   owner,
			Repo:    repo,
			Depth:   0,
		})
	}
	return q.Enqueue(items)
}
