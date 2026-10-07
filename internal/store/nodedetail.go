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
	// Owned is whether the declaration covers the node's repository; Origin never says so.
	Owned      bool   `json:"owned"`
	Path       string `json:"path"`
	IsExternal bool   `json:"isExternal"`
	Analyzed   bool   `json:"analyzed"`
	// RefsInUse and RefsAnalyzed say how much of the dependency was looked at. Analyzed alone is
	// true as soon as one reference has results.
	RefsInUse    int `json:"refsInUse"`
	RefsAnalyzed int `json:"refsAnalyzed"`

	DependentRepos int `json:"dependentRepos"`

	// NamedBy counts the uses: lines on record that name this node, at any reference. Zero for a
	// workflow is a measurement, since its repository was read whole: only its triggers run it.
	NamedBy int `json:"namedBy"`

	// Rules are this node's findings grouped by the rule that produced them.
	Rules []RuleCount `json:"rules"`
	// Uses is what this node pulls in, folded to what a reader recognizes. Who uses it, at which
	// reference and commit, is the walk's answer and not a list of rows: see graph.UsesOf.
	Uses []NodeNeighbor `json:"uses"`
	// RulesUnread and UsesUnread are the references asked for at whose commit nothing was read for
	// that section: nothing resolved them, or no run analyzed that commit. Not analyzed, not clean.
	RulesUnread []string `json:"rulesUnread"`
	UsesUnread  []string `json:"usesUnread"`
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

// NodeDetailOf reads one node by key. With references, what its own code holds and what it uses
// are read at the commits those resolve to rather than across every version analyzed.
func (s *Store) NodeDetailOf(key string, at []string) (NodeDetail, error) {
	var d NodeDetail
	var repo, origin, path sql.NullString
	err := s.db.QueryRow(`
		SELECT n.node_key, n.node_kind, r.repository_full_name, r.origin, `+ownedNode("n")+`, n.path,
		       n.is_external,
		       COALESCE(b.dependent_repos, 0),
		       EXISTS (SELECT 1 FROM abom_frontier f
		                WHERE f.node_key = n.node_key AND f.status = 'done'),
		       (SELECT COUNT(*) FROM abom_frontier f WHERE f.node_key = n.node_key),
		       (SELECT COUNT(*) FROM abom_frontier f
		         WHERE f.node_key = n.node_key AND f.status = 'done'),
		       (SELECT COUNT(*) FROM `+edgeSource(AnalysisModeRegular)+` e
		         WHERE e.dst_node_row_id = n.row_id AND e.edge_kind IN (?, ?, ?))
		FROM abom_node_live n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		WHERE n.node_key = ?`,
		EdgeKindStepUsesAction, EdgeKindJobUsesReusableWorkflow, EdgeKindCompositeStepUsesAction, key).
		Scan(&d.Key, &d.Kind, &repo, &origin, &d.Owned, &path, &d.IsExternal,
			&d.DependentRepos, &d.Analyzed,
			&d.RefsInUse, &d.RefsAnalyzed, &d.NamedBy)
	if err == sql.ErrNoRows {
		return d, fmt.Errorf("no node %q", key)
	}
	if err != nil {
		return d, fmt.Errorf("reading node %q: %w", key, err)
	}
	d.Repository, d.Origin, d.Path = repo.String, origin.String, path.String

	var findings []int64
	var commits []string
	d.RulesUnread, d.UsesUnread = []string{}, []string{}
	if len(at) > 0 {
		if findings, commits, err = s.readAt(&d, at); err != nil {
			return d, err
		}
	}
	if d.Rules, err = s.nodeRules(key, findings, len(at) > 0); err != nil {
		return d, err
	}
	if d.Uses, err = s.nodeUses(key, commits, len(at) > 0); err != nil {
		return d, err
	}
	return d, nil
}

// readAt resolves the references asked for to commits, and returns the findings runs reported in
// the node's own code there and the commits whose declarations of what it uses were read. A
// reference neither section could read is named in that section's unread list.
func (s *Store) readAt(d *NodeDetail, at []string) ([]int64, []string, error) {
	var repository string
	if err := s.db.QueryRow(`SELECT LOWER(COALESCE(owner, '') || '/' || COALESCE(repo, ''))
		FROM abom_node_live WHERE node_key = ?`, d.Key).Scan(&repository); err != nil {
		return nil, nil, fmt.Errorf("reading the repository of %q: %w", d.Key, err)
	}
	args := make([]any, len(at))
	for i, ref := range at {
		args[i] = ref
	}
	resolved := map[string]string{}
	rows, err := queryAll(s, "resolving references", func(r *sql.Rows) ([2]string, error) {
		var p [2]string
		return p, r.Scan(&p[0], &p[1])
	}, `SELECT COALESCE(ref, ''), LOWER(resolved_sha) FROM abom_frontier
	     WHERE node_key = ? AND COALESCE(resolved_sha, '') <> ''
	       AND COALESCE(ref, '') IN (`+placeholders(len(at))+`)`,
		append([]any{d.Key}, args...)...)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range rows {
		resolved[p[0]] = p[1]
	}
	findings, commits := []int64{}, []string{}
	for _, ref := range at {
		commit := resolved[ref]
		ids, reached := []int64(nil), false
		if commit != "" {
			if ids, reached, err = s.findingIDsAtCommit(d.Key, repository, commit); err != nil {
				return nil, nil, err
			}
		}
		if reached {
			findings = append(findings, ids...)
		} else {
			d.RulesUnread = append(d.RulesUnread, ref)
		}
		var declared []int64
		if commit != "" {
			if declared, err = s.runsAtCommit(repository, commit, edgesRun("q")); err != nil {
				return nil, nil, err
			}
		}
		if len(declared) > 0 {
			commits = append(commits, commit)
		} else {
			d.UsesUnread = append(d.UsesUnread, ref)
		}
	}
	return findings, commits, nil
}

// nodeRules counts the node's findings by rule; narrowed, only the findings listed.
func (s *Store) nodeRules(key string, findings []int64, narrowed bool) ([]RuleCount, error) {
	only, args := "", []any{key}
	if narrowed {
		list, ids := idList(findings)
		only, args = ` AND f.row_id IN `+list, append(args, ids...)
	}
	rows, err := s.db.Query(`
		SELECT rule_id, COUNT(*), GROUP_CONCAT(sources), COUNT(*) - COUNT(sources), `+
		bandSums("score")+`
		FROM (SELECT f.rule_id, `+findingSourcesIn(AnalysisModeRegular)+` AS sources, f.security_severity AS score
		      FROM abom_finding_node fn
		      JOIN abom_node_live n ON n.row_id = fn.node_row_id
		      JOIN abom_finding_regular f ON f.row_id = fn.finding_row_id
		      WHERE n.node_key = ?`+only+`)
		GROUP BY rule_id
		ORDER BY COUNT(*) DESC, rule_id`, args...)
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
// names things a reader recognizes. The lines are its own or those of the steps and jobs it holds.
// Narrowed, they are read as the commits listed declared them rather than as the last run did.
func (s *Store) nodeUses(key string, commits []string, narrowed bool) ([]NodeNeighbor, error) {
	// What its uses: lines name. `needs:` orders a workflow's own jobs and runs nothing.
	uses := []any{EdgeKindStepUsesAction, EdgeKindJobUsesReusableWorkflow, EdgeKindCompositeStepUsesAction}
	from, args := `(SELECT e.src_node_row_id, e.dst_node_row_id, COALESCE(e.ref_value, '') AS ref
		FROM abom_edge_regular e WHERE e.edge_kind IN (?, ?, ?))`, uses
	if narrowed {
		list, at := inList(commits)
		from, args = `(SELECT src_node_row_id, dst_node_row_id, ref FROM observed
		 WHERE edge_kind IN (?, ?, ?) AND commit_sha IN `+list+`)`, append(uses, at...)
	}
	rows, err := s.db.Query(`WITH `+observedEdges+`
		SELECT shown.node_key, shown.node_kind, COALESCE(r.repository_full_name, ''),
		       COALESCE(r.origin, ''), COUNT(*), COALESCE(GROUP_CONCAT(DISTINCT NULLIF(e.ref, '')), '')
		FROM `+from+` e
		JOIN abom_node_live source ON source.row_id = e.src_node_row_id
		JOIN abom_node_live node ON node.node_key = ?
		JOIN abom_node_live far ON far.row_id = e.dst_node_row_id
		JOIN abom_node_live shown ON shown.row_id = COALESCE(far.container_node_row_id, far.row_id)
		LEFT JOIN repository r ON r.row_id = shown.repository_row_id
		WHERE source.row_id = node.row_id OR source.container_node_row_id = node.row_id
		GROUP BY shown.node_key, shown.node_kind, r.repository_full_name, r.origin
		ORDER BY COUNT(*) DESC, shown.node_key
		LIMIT 500`, append(args, key)...)
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
