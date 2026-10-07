package store

import (
	"database/sql"
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
	// ReadAt is the commit this entry's own dependencies were read at: the locked one, or the one
	// its reference resolved to. Empty where nothing resolved it, which is unanalyzed.
	ReadAt string
}

// observedEdges is what each regular run's commit declared at each location, as a CTE named
// `observed`: every sighting whose content was recorded, and the row itself at the commit of the
// run that wrote it, which is "" for a row no run recorded. A sighting whose content was never
// recorded says nothing, so it is not here at all.
const observedEdges = `observed(edge_row_id, src_node_row_id, dst_node_row_id, ref, commit_sha,
         repository_row_id, file_path, edge_kind) AS (
	SELECT sg.edge_row_id, e.src_node_row_id, sg.dst_node_row_id, COALESCE(sg.ref_value, ''),
	       LOWER(COALESCE(q.db_commit, '')), e.repository_row_id, e.file_path, e.edge_kind
	  FROM abom_edge_sighting sg
	  JOIN abom_query_run q ON q.row_id = sg.query_run_id AND q.analysis_mode = 'regular'
	  JOIN abom_edge_regular e ON e.row_id = sg.edge_row_id
	 WHERE sg.dst_node_row_id IS NOT NULL
	UNION
	SELECT e.row_id, e.src_node_row_id, e.dst_node_row_id, COALESCE(e.ref_value, ''),
	       LOWER(COALESCE(q.db_commit, '')), e.repository_row_id, e.file_path, e.edge_kind
	  FROM abom_edge_regular e
	  LEFT JOIN abom_query_run q ON q.row_id = e.query_run_id
	 WHERE COALESCE(q.analysis_mode, 'regular') = 'regular'
)`

// submissionClosureQuery walks a repository's workflows outward into what those workflows
// actually run.
//
// A dependency is left only through what its reached container holds: a composite action's steps,
// or a called reusable workflow's jobs and steps. Edges are attributed to the repository whose
// database produced them, so a dependency's own CI hangs from its own workflow nodes and is never
// reached.
//
// The step inside a composite action is joined by `container_node_row_id`, not by rebuilding a key
// from owner and path, which diverge once the manifest filename is stripped.
//
// A child is taken only from what the commit the consumer actually runs declared. ABOM analyzes
// many refs of a popular action, and without this the closure reports the union of every version's
// children. The commit is the one the repository's current lock selects where a lock governs the
// route (?2, the choices of this repository's locks, by edge), and otherwise the one the declared
// ref resolved to; a commit settled as identical to an analyzed one reads that one's (?3, which
// analyzed commits answer for each resolved commit). What it declared is read per sighting, never
// from the row: a location is one row however many commits declare it, and the row holds the last
// writer's target. The repository's own declarations are read at its default branch (?4, the
// commits answering for it, NULL where no reference resolves the repository), so neither a step it
// has since deleted nor what a release only a consumer pins declared is submitted as its own.
const submissionClosureQuery = `
WITH RECURSIVE lock_choice(edge_row_id, commit_sha) AS (
	SELECT CAST(json_extract(j.value, '$.e') AS INTEGER), LOWER(json_extract(j.value, '$.c'))
	  FROM json_each(?2) j
),
live_answer(repository, commit_sha, answer) AS (
	SELECT json_extract(j.value, '$.r'), json_extract(j.value, '$.c'), json_extract(j.value, '$.a')
	  FROM json_each(?3) j
),
` + observedEdges + `,
own AS (
	SELECT o.* FROM observed o
	  JOIN repository r ON r.row_id = o.repository_row_id
	 WHERE r.repository_full_name = ?1
	   AND (?4 IS NULL OR o.commit_sha IN (SELECT j.value FROM json_each(?4) j))
),
closure(manifest, node_row, node_key, owner, repo, subpath, ref, locked, pin, depth, parent_key) AS (
	SELECT o.file_path, n.row_id, n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''),
	       COALESCE(n.path,''), o.ref, COALESCE(lc.commit_sha, ''),
	       COALESCE(lc.commit_sha, (SELECT LOWER(f.resolved_sha) FROM abom_frontier f
	                                 WHERE f.node_key = n.node_key AND f.ref = o.ref)),
	       0, ''
	  FROM own o
	  JOIN abom_node_live n ON n.row_id = o.dst_node_row_id
	  LEFT JOIN lock_choice lc ON lc.edge_row_id = o.edge_row_id
	 WHERE n.is_external = 1
	   AND o.file_path LIKE '.github/workflows/%'
	UNION
	-- The repository's own composite actions. These edges never leave the repository, so nothing
	-- reaches them by walking outward from a dependency, and they are the commonest invisible
	-- dependency there is: an action.yml is not a manifest to GitHub.
	SELECT o.file_path, n.row_id, n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''),
	       COALESCE(n.path,''), o.ref, COALESCE(lc.commit_sha, ''),
	       COALESCE(lc.commit_sha, (SELECT LOWER(f.resolved_sha) FROM abom_frontier f
	                                 WHERE f.node_key = n.node_key AND f.ref = o.ref)),
	       1, ''
	  FROM own o
	  JOIN abom_node_live n ON n.row_id = o.dst_node_row_id
	  LEFT JOIN lock_choice lc ON lc.edge_row_id = o.edge_row_id
	 WHERE n.is_external = 1
	   AND o.edge_kind = 'composite-step-uses-action'
	UNION
	-- A route a lock governs stays governed: the root lock selects every commit under it. One it
	-- does not govern is resolved by its declared refs all the way down.
	SELECT c.manifest, dst.row_id, dst.node_key, COALESCE(dst.owner,''), COALESCE(dst.repo,''),
	       COALESCE(dst.path,''), o.ref,
	       CASE WHEN c.locked <> '' THEN COALESCE(lc.commit_sha, '') ELSE '' END,
	       CASE WHEN c.locked <> '' AND COALESCE(lc.commit_sha, '') <> '' THEN lc.commit_sha
	            ELSE (SELECT LOWER(f.resolved_sha) FROM abom_frontier f
	                   WHERE f.node_key = dst.node_key AND f.ref = o.ref) END,
	       c.depth + 1, c.node_key
	  FROM closure c
	  JOIN abom_node_live st ON st.container_node_row_id = c.node_row
	  JOIN observed o ON o.src_node_row_id = st.row_id AND o.edge_kind <> 'job-needs-job'
	  JOIN abom_node_live dst ON dst.row_id = o.dst_node_row_id
	  LEFT JOIN lock_choice lc ON lc.edge_row_id = o.edge_row_id
	 WHERE c.depth < 10
	   AND o.commit_sha IN (
	     SELECT la.answer FROM live_answer la
	      WHERE la.repository = LOWER(c.owner || '/' || c.repo) AND la.commit_sha = c.pin
	     UNION SELECT c.pin)
)
SELECT c.manifest, c.owner, c.repo, c.subpath, c.ref, c.depth, c.parent_key,
       COALESCE((` + resolvedVersionForRef + `), ''), c.locked, COALESCE(c.pin, '')
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
	return s.closureAt(fullName, false)
}

// StartingPointClosure is SubmissionClosure at every commit reach counts, protected branches included.
func (s *Store) StartingPointClosure(fullName string) ([]ClosureEntry, error) {
	return s.closureAt(fullName, true)
}

func (s *Store) closureAt(fullName string, branches bool) ([]ClosureEntry, error) {
	live, err := s.LiveCommits()
	if err != nil {
		return nil, err
	}
	own, governed, err := s.snapshotCommits(fullName, live, branches)
	if err != nil {
		return nil, err
	}
	return s.submissionClosure(fullName, live, own, governed)
}

// snapshotCommits are the commits a snapshot reads a repository's own declarations at:
// StartingCommits' for its default branch, which is what a snapshot is filed under. What only a
// protected branch declares is not a dependency of that commit. governed is false where no
// reference resolves the repository, which keeps every commit it was analyzed at.
func (s *Store) snapshotCommits(
	fullName string, live map[string]map[string][]string, branches bool,
) (own map[string][]string, governed bool, err error) {
	rows, err := s.liveRows()
	if err != nil {
		return nil, false, err
	}
	own, governed = startingCommits(rows, live, branches)[strings.ToLower(fullName)]
	return own, governed, nil
}

func (s *Store) submissionClosure(
	fullName string, live map[string]map[string][]string, own map[string][]string, governed bool,
) ([]ClosureEntry, error) {
	choices, err := s.lockChoicesOf(fullName)
	if err != nil {
		return nil, err
	}
	answers, err := liveAnswers(live, nil)
	if err != nil {
		return nil, err
	}
	var ownAnswers sql.NullString
	if governed {
		read := []string{}
		for _, by := range own {
			read = append(read, by...)
		}
		body, err := json.Marshal(read)
		if err != nil {
			return nil, fmt.Errorf("encoding the commits %s is read at: %w", fullName, err)
		}
		ownAnswers = sql.NullString{String: string(body), Valid: true}
	}
	rows, err := s.DB().Query(submissionClosureQuery, fullName, choices, answers, ownAnswers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ClosureEntry
	for rows.Next() {
		var e ClosureEntry
		var depth int
		if err := rows.Scan(&e.Manifest, &e.Owner, &e.Repo, &e.Subpath, &e.Ref, &depth,
			&e.ParentKey, &e.Version, &e.LockedCommit, &e.ReadAt); err != nil {
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

// liveAnswers is LiveCommits as the JSON array the closure query and liveDeclared read, each commit
// flagged `s` where it is one of StartingCommits'. A commit nothing answers for is listed with an
// empty answer, so its repository still reads as one a reference resolves. The closure reads its
// own repository's starting commits separately and no flag, so it passes no starting commits.
func liveAnswers(live, starting map[string]map[string][]string) (string, error) {
	type answer struct {
		Repository string `json:"r"`
		Commit     string `json:"c"`
		Answer     string `json:"a"`
		Starting   bool   `json:"s,omitempty"`
	}
	out := []answer{}
	for repository, commits := range live {
		for commit, by := range commits {
			_, start := starting[repository][commit]
			if len(by) == 0 {
				out = append(out, answer{repository, commit, "", start})
			}
			for _, a := range by {
				out = append(out, answer{repository, commit, a, start})
			}
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encoding the commits a reference resolves to: %w", err)
	}
	return string(body), nil
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
