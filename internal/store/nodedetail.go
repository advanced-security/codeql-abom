package store

import (
	"database/sql"
	"fmt"
)

// RefUsage is one reference and how often it is used.
//
// Verbatim and unjudged: whether a reference is a moving tag or a pinned commit is a lexical
// property the interface may show, never a verdict this package derives.
type RefUsage struct {
	Ref   string `json:"ref"`
	Count int    `json:"count"`
	// Repositories is how many distinct repositories use this exact reference.
	Repositories int `json:"repositories"`
}

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
	// true as soon as one reference has results, which on this corpus is routinely 1 of 27.
	RefsInUse    int `json:"refsInUse"`
	RefsAnalyzed int `json:"refsAnalyzed"`

	DependentRepos   int `json:"dependentRepos"`
	DirectDependents int `json:"directDependents"`

	// Refs are every reference pointing at this node, most used first.
	Refs []RefUsage `json:"refs"`
	// Rules are this node's findings grouped by the rule that produced them.
	Rules []RuleCount `json:"rules"`
	// Advisories name this node's repository. They are reported beside the observed references
	// rather than applied to them: whether a reference falls inside a range is not decided here.
	Advisories []AdvisoryReach `json:"advisories"`

	UsedBy []NodeNeighbor `json:"usedBy"`
	Uses   []NodeNeighbor `json:"uses"`
}

// AffectedRepo is one repository that would be touched by a change to a node.
type AffectedRepo struct {
	FullName string `json:"fullName"`
	Origin   string `json:"origin"`
	// Hops of 1 means the repository names the node itself and can change it; more than 1 means
	// it arrives through an intermediary somebody else owns. Never collapsed into one figure.
	Hops int      `json:"hops"`
	Refs []string `json:"refs"`
}

// BumpImpact is who would be affected by changing a node's version.
type BumpImpact struct {
	Key string `json:"key"`
	// Direct and Transitive are counts of repositories, kept apart because they are different
	// pieces of work: one you can fix, the other waits on an intermediary.
	Direct     int            `json:"direct"`
	Transitive int            `json:"transitive"`
	Repos      []AffectedRepo `json:"repos"`
	// Unanalyzed is how many dependencies have never been expanded. Anything reachable only
	// through one of those cannot appear here at all, and saying so is part of the answer.
	Unanalyzed int `json:"unanalyzed"`
}

// BumpImpactOf reports the repositories affected by changing one node.
//
// Walks the edge table rather than abom_exposure: exposure only holds rows for actions named by
// an advisory, so reading it here returned nothing for the overwhelming majority of nodes while
// looking like a real answer. Hops count edges back from the node, so a repository at hop 1
// names it directly and can change the reference itself.
func (s *Store) BumpImpactOf(key string) (BumpImpact, error) {
	out := BumpImpact{Key: key, Repos: []AffectedRepo{}}

	rows, err := s.db.Query(`
		WITH RECURSIVE reach(node_row_id, hops) AS (
			SELECT row_id, 0 FROM abom_node_live WHERE node_key = ?
			UNION
			SELECT e.src_node_row_id, r.hops + 1
			FROM abom_edge_regular e
			JOIN reach r ON e.dst_node_row_id = r.node_row_id
			WHERE r.hops < 8
			UNION
			-- Containment, at no extra hop. A step is not depended upon directly; the workflow
			-- holding it is, and that workflow may be reusable and called from another
			-- repository. Without this the walk stops at the first step and every result looks
			-- purely direct.
			SELECT n.container_node_row_id, r.hops
			FROM abom_node_live n
			JOIN reach r ON n.row_id = r.node_row_id
			WHERE n.container_node_row_id IS NOT NULL AND r.hops < 8
		)
		SELECT rep.repository_full_name, COALESCE(rep.origin, ''), MIN(reach.hops)
		FROM reach
		JOIN abom_node_live n ON n.row_id = reach.node_row_id
		JOIN repository rep ON rep.row_id = n.repository_row_id
		WHERE reach.hops > 0
		  -- Upstream repositories contribute edges but are not dependents: they were fetched
		  -- only to resolve what the repositories in scope rely on, and nobody would bump a
		  -- version on their behalf. Counting them here inflated this from 202 to 221.
		  AND rep.origin = ?
		GROUP BY rep.repository_full_name, rep.origin
		ORDER BY MIN(reach.hops), rep.repository_full_name`, key, OriginSeed)
	if err != nil {
		return out, fmt.Errorf("reading bump impact for %q: %w", key, err)
	}
	defer rows.Close()

	for rows.Next() {
		var a AffectedRepo
		if err := rows.Scan(&a.FullName, &a.Origin, &a.Hops); err != nil {
			return out, fmt.Errorf("scanning affected repository: %w", err)
		}
		a.Refs = []string{}
		if a.Hops == 1 {
			out.Direct++
		} else {
			out.Transitive++
		}
		out.Repos = append(out.Repos, a)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("reading bump impact: %w", err)
	}

	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM abom_frontier WHERE status != 'done'`).
		Scan(&out.Unanalyzed); err != nil {
		return out, fmt.Errorf("reading unanalyzed count: %w", err)
	}
	if err := s.attachAffectedRefs(key, out.Repos); err != nil {
		return out, err
	}
	return out, nil
}

// attachAffectedRefs records which reference each repository uses.
//
// Only repositories holding an edge straight to the node have one. A repository that reaches it
// through an intermediary has no reference of its own to change, and an empty list is the honest
// way to say so.
func (s *Store) attachAffectedRefs(key string, repos []AffectedRepo) error {
	rows, err := s.db.Query(`
		SELECT rep.repository_full_name, e.ref_value
		FROM abom_edge_regular e
		JOIN abom_node_live dst ON dst.row_id = e.dst_node_row_id
		JOIN repository rep ON rep.row_id = e.repository_row_id
		WHERE dst.node_key = ? AND e.ref_value IS NOT NULL AND e.ref_value <> ''
		GROUP BY rep.repository_full_name, e.ref_value
		ORDER BY rep.repository_full_name, e.ref_value`, key)
	if err != nil {
		return fmt.Errorf("reading references for %q: %w", key, err)
	}
	defer rows.Close()

	byRepo := map[string][]string{}
	for rows.Next() {
		var repo, ref string
		if err := rows.Scan(&repo, &ref); err != nil {
			return fmt.Errorf("scanning affected reference: %w", err)
		}
		byRepo[repo] = append(byRepo[repo], ref)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading affected references: %w", err)
	}

	for i := range repos {
		if refs, ok := byRepo[repos[i].FullName]; ok {
			repos[i].Refs = refs
		}
	}
	return nil
}

// NodeDetailOf reads one node by key.
func (s *Store) NodeDetailOf(key string) (NodeDetail, error) {
	var d NodeDetail
	var repo, origin, path sql.NullString
	err := s.db.QueryRow(`
		SELECT n.node_key, n.node_kind, r.repository_full_name, r.origin, n.path, n.is_external,
		       COALESCE(b.dependent_repos, 0), COALESCE(b.direct_dependents, 0),
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
			&d.DependentRepos, &d.DirectDependents, &d.Analyzed,
			&d.RefsInUse, &d.RefsAnalyzed)
	if err == sql.ErrNoRows {
		return d, fmt.Errorf("no node %q", key)
	}
	if err != nil {
		return d, fmt.Errorf("reading node %q: %w", key, err)
	}
	d.Repository, d.Origin, d.Path = repo.String, origin.String, path.String

	if d.Refs, err = s.refUsage(key); err != nil {
		return d, err
	}
	if d.Rules, err = s.nodeRules(key); err != nil {
		return d, err
	}
	if d.UsedBy, err = s.nodeNeighbors(key, true); err != nil {
		return d, err
	}
	if d.Uses, err = s.nodeNeighbors(key, false); err != nil {
		return d, err
	}
	d.Advisories = []AdvisoryReach{}
	return d, nil
}

// refUsage lists every reference pointing at a node, most used first.
func (s *Store) refUsage(key string) ([]RefUsage, error) {
	rows, err := s.db.Query(`
		SELECT e.ref_value, COUNT(*), COUNT(DISTINCT e.repository_row_id)
		FROM abom_edge_regular e
		JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
		WHERE n.node_key = ? AND e.ref_value IS NOT NULL AND e.ref_value <> ''
		GROUP BY e.ref_value
		ORDER BY COUNT(*) DESC, e.ref_value`, key)
	if err != nil {
		return nil, fmt.Errorf("reading references to %q: %w", key, err)
	}
	defer rows.Close()

	out := []RefUsage{}
	for rows.Next() {
		var u RefUsage
		if err := rows.Scan(&u.Ref, &u.Count, &u.Repositories); err != nil {
			return nil, fmt.Errorf("scanning reference: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) nodeRules(key string) ([]RuleCount, error) {
	rows, err := s.db.Query(`
		SELECT rule_id, COUNT(*), GROUP_CONCAT(sources), COUNT(*) - COUNT(sources), `+
		bandSums("score")+`
		FROM (SELECT f.rule_id, `+findingSources+` AS sources, f.security_severity AS score
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

// nodeNeighbors lists what uses this node, or what it uses, folded to the container that holds
// the reference so the list names things a reader recognizes.
func (s *Store) nodeNeighbors(key string, incoming bool) ([]NodeNeighbor, error) {
	side, other := "dst_node_row_id", "src_node_row_id"
	if !incoming {
		side, other = "src_node_row_id", "dst_node_row_id"
	}

	query := fmt.Sprintf(`
		SELECT shown.node_key, shown.node_kind, COALESCE(r.repository_full_name, ''),
		       COALESCE(r.origin, ''), COUNT(*),
		       COALESCE(GROUP_CONCAT(DISTINCT e.ref_value), '')
		FROM abom_edge_regular e
		JOIN abom_node_live target ON target.row_id = e.%s
		JOIN abom_node_live far ON far.row_id = e.%s
		JOIN abom_node_live shown ON shown.row_id = COALESCE(far.container_node_row_id, far.row_id)
		LEFT JOIN repository r ON r.row_id = shown.repository_row_id
		WHERE target.node_key = ?
		GROUP BY shown.node_key, shown.node_kind, r.repository_full_name, r.origin
		ORDER BY COUNT(*) DESC, shown.node_key
		LIMIT 500`, side, other)

	rows, err := s.db.Query(query, key)
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
