package store

import (
	"fmt"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/normalize"
)

// attachOwnedRepositories adds a node for every declared repository the graph does not already
// hold one for.
//
// A repository produces nodes only when something analyzes it, so one enumerated an hour ago and
// one holding nothing to analyze are both simply absent — and absence reads as an estate made
// only of the parts that succeeded. On the reference corpus this hid **105 of 198** owned
// repositories: 40 still queued and 65 `unavailable`.
//
// Derived at read time rather than stored, for the reason attachOwners gives: it is a view over
// facts the store already holds, and writing it would create a second source of truth for which
// repositories exist.
//
// A property filter applies here too. Backfilling past it would put every declared repository into
// a graph narrowed to one part of the organization — the same silent widening the filter exists to
// prevent, arriving after the query rather than inside it.
func (s *Store) attachOwnedRepositories(nodes []GraphNode, opts FullGraphOptions) ([]GraphNode, error) {
	var admitted map[string]bool
	if opts.GroupFiltered {
		admitted = map[string]bool{}
		for _, name := range opts.GroupClosure {
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
			Path:       fullName,
			Rules:      []string{},
			Facets:     []FindingFacet{},
			Discovery:  discovery,
			Analyzed:   analyzed,
			// Nothing has been ingested for this repository, so nothing measured what depends
			// on it. Reporting reach as known-and-zero would draw it at the size that means
			// "nothing depends on this", which is an assertion nobody earned.
			ReachKnown: false,
		})
	}
	return nodes, rows.Err()
}

// frontierRepoState is frontierState asked of a repository by name, for repositories that have no
// node to key off.
const frontierRepoState = `(SELECT CASE
			 WHEN SUM(f.status = 'running') > 0 THEN 'running'
			 WHEN SUM(f.status = 'pending' AND f.attempts > 0) > 0 THEN 'retrying'
			 WHEN SUM(f.status = 'pending') > 0 THEN 'queued'
			 WHEN SUM(f.status = 'done') > 0 THEN 'done'
			 ELSE MIN(f.status)
		   END FROM abom_frontier f
		  WHERE LOWER(f.node_key) = LOWER(r.repository_full_name))`
