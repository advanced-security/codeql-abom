package store

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ClosureEntry is one package in a repository's Actions supply chain, as the dependency
// submission API needs it: which manifest reached it, how deep, and through what.
type ClosureEntry struct {
	Manifest string
	Owner    string
	Repo     string
	Subpath  string
	Ref      string
	// Version is the tag a commit-pinned reference was released as, empty unless something
	// measured it. GitHub alerts on semantic versions and not on commits, so this is what makes
	// a pinned dependency matchable against an advisory.
	Version      string
	Relationship string
	ParentKey    string
	// LockedCommit is the commit the repository's current lock selects for this entry, empty
	// where no lock governs it. Ref stays as written; this is what runs.
	LockedCommit string
}

// submissionClosureQuery walks a repository's workflows outward into what those workflows
// actually run.
//
// Three rules in here were established by measuring the corpus, and each is easy to get wrong in a
// way that still returns plausible rows:
//
// Only `composite-step-uses-action` leaves a dependency. ABOM attributes an edge to the repository
// whose database produced it, so a dependency's rows also contain how that dependency tests
// itself. codecov/codecov-action carries 74 edges from its own CI and 3 from its action manifest;
// following the former would put 74 actions in a consumer's supply chain that the consumer never
// runs.
//
// The step inside a composite action is joined by `container_node_row_id`, not by rebuilding a key
// from owner and path, which diverge once the manifest filename is stripped.
//
// A child is taken only from an analysis of the commit the consumer actually runs, matched to a
// run that sighted the edge. ABOM analyzes many refs of a popular action, and without this the
// closure reports the union of every version's children: codecov/codecov-action vendors
// actions/github-script from v5.x onward, so an unconstrained walk hands a repository pinning v4 a
// transitive dependency it does not have. The commit is the one the repository's current lock
// selects where a lock governs the route (?2, the choices of this repository's locks, by edge),
// and otherwise the one the declared ref resolved to. Any run that sighted the edge counts, not
// only the latest: an edge two commits share is one row, and its latest run names one of them.
const submissionClosureQuery = `
WITH RECURSIVE lock_choice(edge_row_id, commit_sha) AS (
	SELECT CAST(json_extract(j.value, '$.e') AS INTEGER), LOWER(json_extract(j.value, '$.c'))
	  FROM json_each(?2) j
),
closure(manifest, node_row, node_key, owner, repo, subpath, ref, locked, depth, parent_key) AS (
	SELECT e.file_path, n.row_id, n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''),
	       COALESCE(n.path,''), COALESCE(e.ref_value,''), COALESCE(lc.commit_sha, ''), 0, ''
	  FROM abom_edge_regular e
	  JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	  JOIN repository r ON r.row_id = e.repository_row_id
	  LEFT JOIN lock_choice lc ON lc.edge_row_id = e.row_id
	 WHERE r.repository_full_name = ?1
	   AND n.is_external = 1
	   AND e.file_path LIKE '.github/workflows/%'
	UNION
	-- The repository's own composite actions. These edges never leave the repository, so nothing
	-- reaches them by walking outward from a dependency, and they are the commonest invisible
	-- dependency there is: an action.yml is not a manifest to GitHub.
	SELECT e.file_path, n.row_id, n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''),
	       COALESCE(n.path,''), COALESCE(e.ref_value,''), COALESCE(lc.commit_sha, ''), 1, ''
	  FROM abom_edge_regular e
	  JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	  JOIN repository r ON r.row_id = e.repository_row_id
	  LEFT JOIN lock_choice lc ON lc.edge_row_id = e.row_id
	 WHERE r.repository_full_name = ?1
	   AND n.is_external = 1
	   AND e.edge_kind = 'composite-step-uses-action'
	UNION
	-- A route a lock governs stays governed: the root lock selects every commit under it. One it
	-- does not govern is resolved by its declared refs all the way down.
	SELECT c.manifest, dst.row_id, dst.node_key, COALESCE(dst.owner,''), COALESCE(dst.repo,''),
	       COALESCE(dst.path,''), COALESCE(ce.ref_value,''),
	       CASE WHEN c.locked <> '' THEN COALESCE(lc.commit_sha, '') ELSE '' END,
	       c.depth + 1, c.node_key
	  FROM closure c
	  JOIN abom_node_live st ON st.container_node_row_id = c.node_row
	  JOIN abom_edge_regular ce ON ce.src_node_row_id = st.row_id
	                        AND ce.edge_kind = 'composite-step-uses-action'
	  JOIN abom_node_live dst ON dst.row_id = ce.dst_node_row_id
	  LEFT JOIN lock_choice lc ON lc.edge_row_id = ce.row_id
	 WHERE c.depth < 10
	   AND EXISTS (
	     SELECT 1 FROM abom_query_run q
	      WHERE q.analysis_mode = 'regular'
	        AND (q.row_id = ce.query_run_id
	             OR q.row_id IN (SELECT sg.query_run_id FROM abom_edge_sighting sg
	                              WHERE sg.edge_row_id = ce.row_id))
	        AND LOWER(q.db_commit) = CASE
	              WHEN c.locked <> '' THEN c.locked
	              ELSE (SELECT LOWER(f.resolved_sha) FROM abom_frontier f
	                     WHERE f.node_key = c.node_key AND f.ref = c.ref) END)
)
SELECT c.manifest, c.owner, c.repo, c.subpath, c.ref, c.depth, c.parent_key,
       COALESCE((` + resolvedVersionForRef + `), ''), c.locked
  FROM closure c
 ORDER BY c.manifest, c.depth, c.owner, c.repo, c.subpath, c.ref, c.locked`

// resolvedVersionForRef names the tag a commit was released as: the commit a lock selects, or
// the one the reference names when it is itself a commit. Measured sources only: a trailing
// `# v4.2.2` comment is what somebody intended, and so is the tag a lock says it resolved the
// commit from — a submission built on either would name a dependency the repository does not
// have. The most specific tag wins, because a commit carrying v4, v4.2 and v4.2.2 should be
// submitted as the last of those.
const resolvedVersionForRef = `
	SELECT x.tag FROM abom_ref_resolution x
	 WHERE LOWER(x.owner) = LOWER(c.owner) AND LOWER(x.repo) = LOWER(c.repo)
	   AND x.commit_sha = LOWER(CASE WHEN c.locked <> '' THEN c.locked ELSE c.ref END)
	   AND x.source IN ('crawl','tags-api')
	 ORDER BY LENGTH(x.tag) - LENGTH(REPLACE(x.tag, '.', '')) DESC, LENGTH(x.tag) DESC, x.tag DESC
	 LIMIT 1`

// SubmissionClosure returns the Actions dependencies reachable from a repository's own workflows,
// including the transitive ones GitHub cannot see.
//
// An unknown repository yields no rows rather than an error. The caller must treat that as absent
// rather than as an empty supply chain: submitting an empty closure replaces GitHub's own reading
// of the workflow with nothing.
func (s *Store) SubmissionClosure(fullName string) ([]ClosureEntry, error) {
	choices, err := s.lockChoicesOf(fullName)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB().Query(submissionClosureQuery, fullName, choices)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ClosureEntry
	for rows.Next() {
		var e ClosureEntry
		var depth int
		if err := rows.Scan(&e.Manifest, &e.Owner, &e.Repo, &e.Subpath, &e.Ref, &depth,
			&e.ParentKey, &e.Version, &e.LockedCommit); err != nil {
			return nil, err
		}
		e.Relationship = "indirect"
		if depth == 0 {
			e.Relationship = "direct"
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// lockChoicesOf is what one repository's current locks select, by edge, as the JSON array the
// closure query reads: its own workflows' and composites' steps, and the steps of composites its
// lock reaches through the DAG. Empty when nothing is locked, which leaves every route declared.
func (s *Store) lockChoicesOf(consumer string) (string, error) {
	governed, err := s.LockGovernedEdges()
	if err != nil {
		return "", err
	}
	type choice struct {
		Edge   int64  `json:"e"`
		Commit string `json:"c"`
	}
	out := []choice{}
	for _, g := range governed {
		if strings.EqualFold(g.Consumer, consumer) {
			out = append(out, choice{Edge: g.EdgeID, Commit: g.Commit})
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encoding the lock choices of %s: %w", consumer, err)
	}
	return string(body), nil
}
