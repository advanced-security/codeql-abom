package expand

import (
	"fmt"
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
func SeedFrontier(s *store.Store, q *frontier.Queue) (int, error) {
	// The referencing repository is recorded because it is the only answer to "why is this
	// queued at all", and once the item is analyzed the edge that put it there is no longer
	// distinguishable from any other. Several repositories usually name the same dependency;
	// the first by name is chosen so the answer does not change between runs.
	rows, err := s.DB().Query(`
		SELECT n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''), COALESCE(n.path,''),
		       MIN(r.repository_full_name), COALESCE(e.ref_value, '')
		FROM abom_edge_regular e
		JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE ` + store.OwnedRepositoryFilter + `
		  AND ` + dependencyTarget + `
		GROUP BY n.node_key, n.owner, n.repo, n.path, COALESCE(e.ref_value, '')`)
	if err != nil {
		return 0, fmt.Errorf("reading seed dependencies: %w", err)
	}
	defer rows.Close()

	var items []frontier.Item
	for rows.Next() {
		var it frontier.Item
		if err := rows.Scan(&it.NodeKey, &it.Owner, &it.Repo, &it.Subpath,
			&it.DiscoveredFrom, &it.Ref); err != nil {
			return 0, fmt.Errorf("scanning seed dependency: %w", err)
		}
		fromKey(&it)
		it.Depth = 1
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("reading seed dependencies: %w", err)
	}

	return q.Enqueue(items)
}

// dependencyTarget is what a crawl queues from an edge `e` to a node `n`: an external action or
// workflow with an owner, or an owned repository's own action or workflow named by a remote
// reference. That node is the owned repository's — internal, with no owner, its repo the full
// name — and it still runs at the reference that names it, so it is queued at that reference.
const dependencyTarget = `((n.is_external = 1 AND n.owner IS NOT NULL AND n.repo IS NOT NULL)
	OR (n.is_external = 0 AND COALESCE(e.ref_value, '') <> '' AND e.ref_raw LIKE '%_/_%@_%'
	    AND e.ref_raw NOT LIKE './%' AND e.ref_raw NOT LIKE '../%' AND e.ref_raw NOT LIKE '$/%'
	    AND e.ref_raw NOT LIKE 'docker://%'))`

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
