package expand

import (
	"fmt"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// SeedFrontier populates the frontier from every seed repository's own edges, so expand-all has
// somewhere to start.
//
// enqueueDiscoveries (in expand.go) only ever runs after a repository already in the queue has
// been analyzed; nothing seeds the very first batch. A repository under review needs no such
// precondition; naming a dependency at all is what makes that dependency part of a real supply
// chain, unlike an upstream repository's own CI (see enqueueDiscoveries' doc comment).
func SeedFrontier(s *store.Store, q *frontier.Queue) (int, error) {
	rows, err := s.DB().Query(`
		SELECT DISTINCT n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''), COALESCE(n.path,'')
		FROM abom_edge e
		JOIN abom_node n ON n.row_id = e.dst_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE r.origin = ?
		  AND n.is_external = 1 AND n.owner IS NOT NULL AND n.repo IS NOT NULL`, store.OriginSeed)
	if err != nil {
		return 0, fmt.Errorf("reading seed dependencies: %w", err)
	}
	defer rows.Close()

	var items []frontier.Item
	for rows.Next() {
		var it frontier.Item
		if err := rows.Scan(&it.NodeKey, &it.Owner, &it.Repo, &it.Subpath); err != nil {
			return 0, fmt.Errorf("scanning seed dependency: %w", err)
		}
		it.Depth = 1
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("reading seed dependencies: %w", err)
	}

	return q.Enqueue(items)
}
