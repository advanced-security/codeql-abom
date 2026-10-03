package store

import (
	"database/sql"
	"fmt"
)

// NodeNeighbor is one end of a relationship, as the detail page lists it.
type NodeNeighbor struct {
	Key        string   `json:"key"`
	Kind       string   `json:"kind"`
	Repository string   `json:"repository"`
	Origin     string   `json:"origin"`
	Refs       []string `json:"refs"`
	Count      int      `json:"count"`
}

// NodeDetail is everything known about one node.
type NodeDetail struct {
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	Repository string `json:"repository"`
	Origin     string `json:"origin"`
	Path       string `json:"path"`
	IsExternal bool   `json:"isExternal"`
	Analyzed   bool   `json:"analyzed"`
	// RefsInUse and RefsAnalyzed say how much of the dependency was looked at. Analyzed alone is
	// true as soon as one reference has results.
	RefsInUse    int `json:"refsInUse"`
	RefsAnalyzed int `json:"refsAnalyzed"`

	DependentRepos int `json:"dependentRepos"`

	// Rules are this node's findings grouped by the rule that produced them.
	Rules []RuleCount `json:"rules"`
	// Uses is what this node pulls in, folded to what a reader recognizes. Who uses it, at which
	// reference and commit, is the walk's answer and not a list of rows: see graph.UsesOf.
	Uses []NodeNeighbor `json:"uses"`
}

// UnanalyzedDependencies counts queued dependencies that have not been analyzed. A route that runs
// only through one of them cannot appear in any walk, and saying so is part of every answer about
// who uses something.
func (s *Store) UnanalyzedDependencies() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_frontier WHERE status != 'done'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting unanalyzed dependencies: %w", err)
	}
	return n, nil
}

// NodeDetailOf reads one node by key.
func (s *Store) NodeDetailOf(key string) (NodeDetail, error) {
	var d NodeDetail
	var repo, origin, path sql.NullString
	err := s.db.QueryRow(`
		SELECT n.node_key, n.node_kind, r.repository_full_name, r.origin, n.path, n.is_external,
		       COALESCE(b.dependent_repos, 0),
		       EXISTS (SELECT 1 FROM abom_frontier f
		                WHERE f.node_key = n.node_key AND f.status = 'done'),
		       (SELECT COUNT(*) FROM abom_frontier f WHERE f.node_key = n.node_key),
		       (SELECT COUNT(*) FROM abom_frontier f
		         WHERE f.node_key = n.node_key AND f.status = 'done')
		FROM abom_node_live n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		WHERE n.node_key = ?`, key).
		Scan(&d.Key, &d.Kind, &repo, &origin, &path, &d.IsExternal,
			&d.DependentRepos, &d.Analyzed,
			&d.RefsInUse, &d.RefsAnalyzed)
	if err == sql.ErrNoRows {
		return d, fmt.Errorf("no node %q", key)
	}
	if err != nil {
		return d, fmt.Errorf("reading node %q: %w", key, err)
	}
	d.Repository, d.Origin, d.Path = repo.String, origin.String, path.String

	if d.Rules, err = s.nodeRules(key); err != nil {
		return d, err
	}
	if d.Uses, err = s.nodeUses(key); err != nil {
		return d, err
	}
	return d, nil
}

func (s *Store) nodeRules(key string) ([]RuleCount, error) {
	rows, err := s.db.Query(`
		SELECT rule_id, COUNT(*), GROUP_CONCAT(sources), COUNT(*) - COUNT(sources), `+
		bandSums("score")+`
		FROM (SELECT f.rule_id, `+findingSourcesIn(AnalysisModeRegular)+` AS sources, f.security_severity AS score
		      FROM abom_finding_node fn
		      JOIN abom_node_live n ON n.row_id = fn.node_row_id
		      JOIN abom_finding_regular f ON f.row_id = fn.finding_row_id
		      WHERE n.node_key = ?)
		GROUP BY rule_id
		ORDER BY COUNT(*) DESC, rule_id`, key)
	if err != nil {
		return nil, fmt.Errorf("reading rules for %q: %w", key, err)
	}
	defer rows.Close()

	out := []RuleCount{}
	for rows.Next() {
		var c RuleCount
		var sources sql.NullString
		if err := rows.Scan(append([]any{&c.RuleID, &c.Count, &sources, &c.Unsourced},
			c.Severity.targets()...)...); err != nil {
			return nil, fmt.Errorf("scanning rule count: %w", err)
		}
		c.Sources = distinctSources(sources)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	mostSevereFirst(out)
	return out, nil
}

// nodeUses lists what this node uses, folded to the container that holds the target so the list
// names things a reader recognizes.
func (s *Store) nodeUses(key string) ([]NodeNeighbor, error) {
	rows, err := s.db.Query(`
		SELECT shown.node_key, shown.node_kind, COALESCE(r.repository_full_name, ''),
		       COALESCE(r.origin, ''), COUNT(*),
		       COALESCE(GROUP_CONCAT(DISTINCT e.ref_value), '')
		FROM abom_edge_regular e
		JOIN abom_node_live source ON source.row_id = e.src_node_row_id
		JOIN abom_node_live far ON far.row_id = e.dst_node_row_id
		JOIN abom_node_live shown ON shown.row_id = COALESCE(far.container_node_row_id, far.row_id)
		LEFT JOIN repository r ON r.row_id = shown.repository_row_id
		WHERE source.node_key = ?
		GROUP BY shown.node_key, shown.node_kind, r.repository_full_name, r.origin
		ORDER BY COUNT(*) DESC, shown.node_key
		LIMIT 500`, key)
	if err != nil {
		return nil, fmt.Errorf("reading neighbors of %q: %w", key, err)
	}
	defer rows.Close()

	out := []NodeNeighbor{}
	for rows.Next() {
		var n NodeNeighbor
		var refs string
		if err := rows.Scan(&n.Key, &n.Kind, &n.Repository, &n.Origin, &n.Count, &refs); err != nil {
			return nil, fmt.Errorf("scanning neighbor: %w", err)
		}
		n.Refs = splitRefs(refs)
		out = append(out, n)
	}
	return out, rows.Err()
}

func splitRefs(joined string) []string {
	out := []string{}
	if joined == "" {
		return out
	}
	start := 0
	for i := 0; i <= len(joined); i++ {
		if i == len(joined) || joined[i] == ',' {
			if i > start {
				out = append(out, joined[start:i])
			}
			start = i + 1
		}
	}
	return out
}
