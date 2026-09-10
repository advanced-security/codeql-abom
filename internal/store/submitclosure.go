package store

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
// A child is taken only from the analysis of the ref the consumer actually pinned, matched through
// the frontier's resolved SHA to the query run that produced the edge. ABOM analyzes many refs of
// a popular action, and without this the closure reports the union of every version's children:
// codecov/codecov-action vendors actions/github-script from v5.x onward, so an unconstrained walk
// hands a repository pinning v4 a transitive dependency it does not have.
const submissionClosureQuery = `
WITH RECURSIVE closure(manifest, node_row, node_key, owner, repo, subpath, ref, depth, parent_key) AS (
	SELECT e.file_path, n.row_id, n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''),
	       COALESCE(n.path,''), COALESCE(e.ref_value,''), 0, ''
	  FROM abom_edge_live e
	  JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	  JOIN repository r ON r.row_id = e.repository_row_id
	 WHERE r.repository_full_name = ?
	   AND n.is_external = 1
	   AND e.file_path LIKE '.github/workflows/%'
	UNION
	-- The repository's own composite actions. These edges never leave the repository, so nothing
	-- reaches them by walking outward from a dependency, and they are the commonest invisible
	-- dependency there is: an action.yml is not a manifest to GitHub.
	SELECT e.file_path, n.row_id, n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''),
	       COALESCE(n.path,''), COALESCE(e.ref_value,''), 1, ''
	  FROM abom_edge_live e
	  JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	  JOIN repository r ON r.row_id = e.repository_row_id
	 WHERE r.repository_full_name = ?
	   AND n.is_external = 1
	   AND e.edge_kind = 'composite-step-uses-action'
	UNION
	SELECT c.manifest, dst.row_id, dst.node_key, COALESCE(dst.owner,''), COALESCE(dst.repo,''),
	       COALESCE(dst.path,''), COALESCE(ce.ref_value,''), c.depth + 1, c.node_key
	  FROM closure c
	  JOIN abom_node_live st ON st.container_node_row_id = c.node_row
	  JOIN abom_edge_live ce ON ce.src_node_row_id = st.row_id
	                        AND ce.edge_kind = 'composite-step-uses-action'
	  JOIN abom_node_live dst ON dst.row_id = ce.dst_node_row_id
	  JOIN abom_frontier f ON f.node_key = c.node_key AND f.ref = c.ref
	  JOIN abom_query_run q ON q.row_id = ce.query_run_id
	                       AND q.db_commit = f.resolved_sha
	 WHERE c.depth < 10
)
SELECT c.manifest, c.owner, c.repo, c.subpath, c.ref, c.depth, c.parent_key,
       COALESCE((` + resolvedVersionForRef + `), '')
  FROM closure c
 ORDER BY c.manifest, c.depth, c.owner, c.repo, c.subpath, c.ref`

// resolvedVersionForRef names the tag a commit was released as. Measured sources only: a trailing
// `# v4.2.2` comment is what somebody intended, and a submission built on it would name a
// dependency the repository does not have. The most specific tag wins, because a commit carrying
// v4, v4.2 and v4.2.2 should be submitted as the last of those.
const resolvedVersionForRef = `
	SELECT x.tag FROM abom_ref_resolution x
	 WHERE LOWER(x.owner) = LOWER(c.owner) AND LOWER(x.repo) = LOWER(c.repo)
	   AND x.commit_sha = LOWER(c.ref) AND x.source IN ('crawl','tags-api')
	 ORDER BY LENGTH(x.tag) - LENGTH(REPLACE(x.tag, '.', '')) DESC, LENGTH(x.tag) DESC, x.tag DESC
	 LIMIT 1`

// SubmissionClosure returns the Actions dependencies reachable from a repository's own workflows,
// including the transitive ones GitHub cannot see.
//
// An unknown repository yields no rows rather than an error. The caller must treat that as absent
// rather than as an empty supply chain: submitting an empty closure replaces GitHub's own reading
// of the workflow with nothing.
func (s *Store) SubmissionClosure(fullName string) ([]ClosureEntry, error) {
	rows, err := s.DB().Query(submissionClosureQuery, fullName, fullName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ClosureEntry
	for rows.Next() {
		var e ClosureEntry
		var depth int
		if err := rows.Scan(&e.Manifest, &e.Owner, &e.Repo, &e.Subpath, &e.Ref, &depth,
			&e.ParentKey, &e.Version); err != nil {
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
