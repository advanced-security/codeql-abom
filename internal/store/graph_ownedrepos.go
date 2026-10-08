package store

import (
	"fmt"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/normalize"
)

// attachOwnedRepositories adds missing declared repositories without widening group filters.
// It is derived at read time to avoid a second source of repository existence.
func (s *Store) attachOwnedRepositories(nodes []GraphNode, opts FullGraphOptions) ([]GraphNode, error) {
	var admitted map[string]bool
	if opts.GroupFiltered {
		admitted = map[string]bool{}
		for _, name := range opts.GroupSources {
			admitted[strings.ToLower(name)] = true
		}
	}

	drawn := map[string]bool{}
	for _, n := range nodes {
		if n.Kind == "repo" && n.Repository != "" {
			drawn[n.Repository] = true
		}
	}

	rows, err := s.db.Query(`SELECT r.repository_full_name, r.origin,
		       COALESCE(` + frontierRepoState + `, ''),
		       EXISTS (SELECT 1 FROM abom_frontier f
		                WHERE LOWER(f.node_key) = LOWER(r.repository_full_name)
		                  AND f.status = 'done')
		  FROM repository r
		 WHERE ` + OwnedRepositoryFilter + `
		 ORDER BY r.repository_full_name`)
	if err != nil {
		return nodes, fmt.Errorf("reading declared repositories: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var fullName, origin, discovery string
		var analyzed bool
		if err := rows.Scan(&fullName, &origin, &discovery, &analyzed); err != nil {
			return nodes, fmt.Errorf("scanning a declared repository: %w", err)
		}
		if drawn[fullName] {
			continue
		}
		if admitted != nil && !admitted[strings.ToLower(fullName)] {
			continue
		}
		nodes = append(nodes, GraphNode{
			Key:        normalize.RepoKey(fullName),
			Kind:       "repo",
			Repository: fullName,
			Origin:     origin,
			// Selected by the declaration's own predicate, so it is yours by construction.
			Owned:     true,
			Path:      fullName,
			Rules:     []string{},
			Facets:    []FindingFacet{},
			Discovery: discovery,
			Analyzed:  analyzed,
			// Reach is unknown; zero would assert that nothing depends on this repository.
			ReachKnown: false,
		})
	}
	return nodes, rows.Err()
}

// frontierRepoState is frontierState asked of a repository by name, for repositories that have no
// node to key off.
const frontierRepoState = `(SELECT CASE
			 WHEN SUM(f.status = 'running') > 0 THEN 'running'
			 WHEN SUM(f.status = 'pending' AND f.attempts > 0
			          AND f.awaiting_code_scanning IS NULL) > 0 THEN 'retrying'
			 WHEN SUM(f.status = 'pending' AND f.awaiting_code_scanning IS NOT NULL) > 0 THEN 'waiting'
			 WHEN SUM(f.status = 'pending') > 0 THEN 'queued'
			 WHEN SUM(f.status = 'done') > 0 THEN 'done'
			 WHEN SUM(f.status = 'unavailable') > 0 THEN 'unavailable'
			 ELSE MIN(f.status)
		   END FROM abom_frontier f
		  WHERE LOWER(f.node_key) = LOWER(r.repository_full_name))`
