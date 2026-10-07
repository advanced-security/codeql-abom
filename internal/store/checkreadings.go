package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// SnapshotInto writes a consistent copy of the database to path, through the write-ahead log.
func (s *Store) SnapshotInto(path string) error {
	if _, err := s.db.Exec(`VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("copying the database to %s: %w", path, err)
	}
	return nil
}

// DerivedTable is one table derivation maintains, each row as text under what it is about.
type DerivedTable struct {
	Name string
	Rows []DerivedRow
}

// DerivedRow is one maintained row: the key of the node or edge it is about, and the rest as text.
type DerivedRow struct {
	Subject, Row string
}

// derivedReads omit exposure's row id, a surrogate each path reassigns.
var derivedReads = []struct{ name, query string }{
	{"abom_blast_radius", `SELECT COALESCE(n.node_key, '#' || b.node_row_id), b.dependent_repos,
		b.dependent_workflows, b.max_depth, b.refs_in_use, b.dependent_steps, b.dependent_jobs
		FROM abom_blast_radius b LEFT JOIN abom_node_live n ON n.row_id = b.node_row_id`},
	{"abom_node_reach", `SELECT COALESCE(n.node_key, '#' || x.node_row_id),
		COALESCE(r.repository_full_name, '#' || x.repository_row_id), x.hops, x.steps, x.jobs,
		x.workflows
		FROM abom_node_reach x LEFT JOIN abom_node_live n ON n.row_id = x.node_row_id
		LEFT JOIN repository r ON r.row_id = x.repository_row_id`},
	{"abom_node_version_reach", `SELECT COALESCE(n.node_key, '#' || v.node_row_id), v.commit_sha,
		COALESCE(r.repository_full_name, '#' || v.repository_row_id), v.hops
		FROM abom_node_version_reach v LEFT JOIN abom_node_live n ON n.row_id = v.node_row_id
		LEFT JOIN repository r ON r.row_id = v.repository_row_id`},
	{"abom_exposure", `SELECT COALESCE(n.node_key, '#' || x.node_row_id),
		COALESCE(r.repository_full_name, '#' || x.repository_row_id), x.ghsa_id, x.hops, x.path
		FROM abom_exposure x LEFT JOIN abom_node_live n ON n.row_id = x.node_row_id
		LEFT JOIN repository r ON r.row_id = x.repository_row_id`},
	// Every edge, whatever mode observed it: derivation gives each one the fact.
	{"abom_edge.advisory_route", `SELECT e.edge_key, e.advisory_route FROM abom_edge_any_mode e`},
}

// DerivedRows reads every row derivation maintains, table by table in a fixed order.
func (s *Store) DerivedRows() ([]DerivedTable, error) {
	out := make([]DerivedTable, 0, len(derivedReads))
	for _, read := range derivedReads {
		rows, err := queryAll(s, "reading "+read.name, scanDerivedRow, read.query)
		if err != nil {
			return nil, err
		}
		out = append(out, DerivedTable{Name: read.name, Rows: rows})
	}
	return out, nil
}

// scanDerivedRow renders the columns after the first as text, NULL kept apart from zero.
func scanDerivedRow(r *sql.Rows) (DerivedRow, error) {
	columns, err := r.Columns()
	if err != nil {
		return DerivedRow{}, err
	}
	values := make([]any, len(columns))
	into := make([]any, len(columns))
	for i := range values {
		into[i] = &values[i]
	}
	if err := r.Scan(into...); err != nil {
		return DerivedRow{}, err
	}
	parts := make([]string, len(values))
	for i, v := range values {
		switch v := v.(type) {
		case nil:
			parts[i] = "NULL"
		case []byte:
			parts[i] = string(v)
		default:
			parts[i] = fmt.Sprint(v)
		}
	}
	return DerivedRow{Subject: parts[0], Row: strings.Join(parts[1:], "|")}, nil
}

// RepositoryReach reads the external nodes each repository's reach names, and its exposure's.
func (s *Store) RepositoryReach() (reach, exposure map[string]map[string]bool, err error) {
	read := func(what, query string) (map[string]map[string]bool, error) {
		pairs, err := queryAll(s, what, func(r *sql.Rows) ([2]string, error) {
			var p [2]string
			return p, r.Scan(&p[0], &p[1])
		}, query)
		if err != nil {
			return nil, err
		}
		out := map[string]map[string]bool{}
		for _, p := range pairs {
			if out[p[0]] == nil {
				out[p[0]] = map[string]bool{}
			}
			out[p[0]][p[1]] = true
		}
		return out, nil
	}
	if reach, err = read("reading reach by repository", `
		SELECT r.repository_full_name, n.node_key FROM abom_node_reach x
		  JOIN abom_node_live n ON n.row_id = x.node_row_id AND n.is_external = 1
		  JOIN repository r ON r.row_id = x.repository_row_id`); err != nil {
		return nil, nil, err
	}
	if exposure, err = read("reading exposure by repository", `
		SELECT DISTINCT r.repository_full_name, n.node_key FROM abom_exposure x
		  JOIN abom_node_live n ON n.row_id = x.node_row_id
		  JOIN repository r ON r.row_id = x.repository_row_id`); err != nil {
		return nil, nil, err
	}
	return reach, exposure, nil
}

// StoredReach reads reach hops by node and repository, and the per-version breakdown united to it.
func (s *Store) StoredReach() (reach, versions map[int64]map[int64]int, err error) {
	read := func(what, query string) (map[int64]map[int64]int, error) {
		rows, err := queryAll(s, what, func(r *sql.Rows) ([3]int64, error) {
			var v [3]int64
			return v, r.Scan(&v[0], &v[1], &v[2])
		}, query)
		if err != nil {
			return nil, err
		}
		out := map[int64]map[int64]int{}
		for _, v := range rows {
			if out[v[0]] == nil {
				out[v[0]] = map[int64]int{}
			}
			out[v[0]][v[1]] = int(v[2])
		}
		return out, nil
	}
	if reach, err = read("reading stored reach",
		`SELECT node_row_id, repository_row_id, hops FROM abom_node_reach`); err != nil {
		return nil, nil, err
	}
	if versions, err = read("reading stored reach by version", `SELECT node_row_id,
		repository_row_id, MIN(hops) FROM abom_node_version_reach GROUP BY 1, 2`); err != nil {
		return nil, nil, err
	}
	return reach, versions, nil
}

// RepositoryNames names every repository by row id.
func (s *Store) RepositoryNames() (map[int64]string, error) {
	rows, err := queryAll(s, "naming repositories", func(r *sql.Rows) (struct {
		id   int64
		name string
	}, error) {
		var v struct {
			id   int64
			name string
		}
		return v, r.Scan(&v.id, &v.name)
	}, `SELECT row_id, repository_full_name FROM repository`)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(rows))
	for _, v := range rows {
		out[v.id] = v.name
	}
	return out, nil
}

// RollupLevel is one level of a node's stored reach rollup beside the sum of its breakdown.
type RollupLevel struct {
	Node, Level string
	// HasRow is false where only the breakdown names the node.
	HasRow bool
	// Each side is invalid where it does not store the level.
	Rollup, Breakdown sql.NullInt64
}

// RollupLevels pairs each abom_blast_radius level with its abom_node_reach count or sum.
func (s *Store) RollupLevels() ([]RollupLevel, error) {
	type pair struct{ rollup, breakdown sql.NullInt64 }
	type node struct {
		key    string
		hasRow bool
		levels [4]pair
	}
	rows, err := queryAll(s, "reading reach rollups beside their breakdown",
		func(r *sql.Rows) (node, error) {
			var n node
			p := &n.levels
			return n, r.Scan(&n.key, &n.hasRow, &p[0].rollup, &p[0].breakdown, &p[1].rollup,
				&p[1].breakdown, &p[2].rollup, &p[2].breakdown, &p[3].rollup, &p[3].breakdown)
		}, `
		WITH b AS (
			SELECT node_row_id, COUNT(*) AS n,
			       SUM(steps) AS steps, COUNT(steps) AS has_steps,
			       SUM(jobs) AS jobs, COUNT(jobs) AS has_jobs,
			       SUM(workflows) AS workflows, COUNT(workflows) AS has_workflows
			  FROM abom_node_reach GROUP BY node_row_id
		),
		pairs AS (
			SELECT r.node_row_id, 1 AS has_row, r.dependent_repos AS repos,
			       r.dependent_steps AS steps, r.dependent_jobs AS jobs,
			       r.dependent_workflows AS workflows, COALESCE(b.n, 0) AS b_n,
			       b.steps AS b_steps, b.has_steps, b.jobs AS b_jobs, b.has_jobs,
			       b.workflows AS b_workflows, b.has_workflows
			  FROM abom_blast_radius r LEFT JOIN b ON b.node_row_id = r.node_row_id
			UNION ALL
			SELECT b.node_row_id, 0, NULL, NULL, NULL, NULL, b.n, b.steps, b.has_steps, b.jobs,
			       b.has_jobs, b.workflows, b.has_workflows
			  FROM b WHERE b.node_row_id NOT IN (SELECT node_row_id FROM abom_blast_radius)
		)
		SELECT COALESCE(n.node_key, '#' || p.node_row_id), p.has_row, p.repos, p.b_n,
		       p.steps, CASE WHEN p.b_n = 0 THEN 0 WHEN p.has_steps = p.b_n THEN p.b_steps END,
		       p.jobs, CASE WHEN p.b_n = 0 THEN 0 WHEN p.has_jobs = p.b_n THEN p.b_jobs END,
		       p.workflows,
		       CASE WHEN p.b_n = 0 THEN 0 WHEN p.has_workflows = p.b_n THEN p.b_workflows END
		  FROM pairs p LEFT JOIN abom_node_live n ON n.row_id = p.node_row_id`)
	if err != nil {
		return nil, err
	}
	names := [4]string{"dependent_repos", "dependent_steps", "dependent_jobs", "dependent_workflows"}
	out := []RollupLevel{}
	for _, n := range rows {
		for i, p := range n.levels {
			if !n.hasRow && i > 0 {
				break
			}
			out = append(out, RollupLevel{Node: n.key, Level: names[i], HasRow: n.hasRow,
				Rollup: p.rollup, Breakdown: p.breakdown})
		}
	}
	return out, nil
}

// ReachRow is one stored reach or exposure row, named by its node and repository.
type ReachRow struct {
	Table, Node, Repository string
	// Origin is the repository's, empty where no repository has the row's id.
	Origin string
}

// ReachOutsideStartingPoints counts reach and exposure rows, listing those naming no seed.
func (s *Store) ReachOutsideStartingPoints() (int, []ReachRow, error) {
	var total int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM abom_node_reach)
		+ (SELECT COUNT(*) FROM abom_node_version_reach) + (SELECT COUNT(*) FROM abom_exposure)`).
		Scan(&total); err != nil {
		return 0, nil, fmt.Errorf("counting reach rows: %w", err)
	}
	rows, err := queryAll(s, "reading reach naming no starting point",
		func(r *sql.Rows) (ReachRow, error) {
			var v ReachRow
			return v, r.Scan(&v.Table, &v.Node, &v.Repository, &v.Origin)
		}, `
		SELECT x.t, COALESCE(n.node_key, '#' || x.node_row_id),
		       COALESCE(r.repository_full_name, '#' || x.repository_row_id), COALESCE(r.origin, '')
		  FROM (SELECT 'abom_node_reach' AS t, node_row_id, repository_row_id FROM abom_node_reach
		        UNION ALL SELECT 'abom_node_version_reach', node_row_id, repository_row_id
		          FROM abom_node_version_reach
		        UNION ALL SELECT 'abom_exposure', node_row_id, repository_row_id FROM abom_exposure) x
		  LEFT JOIN repository r ON r.row_id = x.repository_row_id
		  LEFT JOIN abom_node_live n ON n.row_id = x.node_row_id
		 WHERE COALESCE(r.origin, '') <> ?
		 ORDER BY 1, 2, 3`, OriginSeed)
	return total, rows, err
}

// KindChanges counts locations sighted at several commits, by whether a starting point walks them.
type KindChanges struct {
	Walked, Unwalked int
	Changes          []KindChange
}

// KindChange is a location whose sightings name a job at one commit and something else at another.
type KindChange struct {
	Location, Repository string
	Walked               bool
	// Targets lists each kind, key and commit sighted, as `kind key@commit`.
	Targets string
}

// KindChanges reads where a location's target changed between a job and a non-job across commits.
func (s *Store) KindChanges() (KindChanges, error) {
	type location struct {
		KindChange
		changes bool
	}
	rows, err := queryAll(s, "reading target kinds across commits",
		func(r *sql.Rows) (location, error) {
			var l location
			return l, r.Scan(&l.Location, &l.Repository, &l.Walked, &l.changes, &l.Targets)
		}, `
		WITH s AS (
			SELECT sg.edge_row_id, LOWER(COALESCE(q.db_commit, '')) AS commit_sha, n.node_kind,
			       n.node_key
			  FROM abom_edge_sighting sg
			  JOIN abom_query_run q ON q.row_id = sg.query_run_id AND q.analysis_mode = 'regular'
			  JOIN abom_node_live n ON n.row_id = sg.dst_node_row_id
		),
		multi AS (
			SELECT edge_row_id, MAX(node_kind = 'job') AND MAX(node_kind <> 'job') AS changes,
			       GROUP_CONCAT(DISTINCT node_kind || ' ' || node_key || '@' || commit_sha) AS targets
			  FROM s GROUP BY edge_row_id HAVING COUNT(DISTINCT commit_sha) > 1
		),
		reached(row_id) AS MATERIALIZED (SELECT DISTINCT node_row_id FROM abom_node_reach)
		SELECT e.edge_key, COALESCE(r.repository_full_name, ''),
		       COALESCE(r.origin, '') = ? OR src.container_node_row_id IN (SELECT row_id FROM reached),
		       m.changes, CASE WHEN m.changes THEN m.targets ELSE '' END
		  FROM multi m
		  JOIN abom_edge_any_mode e ON e.row_id = m.edge_row_id
		  JOIN abom_node_live src ON src.row_id = e.src_node_row_id
		  LEFT JOIN repository r ON r.row_id = e.repository_row_id
		 ORDER BY 1`, OriginSeed)
	if err != nil {
		return KindChanges{}, err
	}
	out := KindChanges{Changes: []KindChange{}}
	for _, l := range rows {
		if l.Walked {
			out.Walked++
		} else {
			out.Unwalked++
		}
		if l.changes {
			out.Changes = append(out.Changes, l.KindChange)
		}
	}
	return out, nil
}

// LiveCommitEvidence is the ledger for one live commit, read apart from the walk's reading.
type LiveCommitEvidence struct {
	Commit string
	// Answers counts the analyzed commits standing for it: itself, else each with identical content.
	Answers int
	// Content is whether any of those runs kept what each commit declared.
	Content bool
	// Analyzed counts the commits its repository's edges runs analyzed.
	Analyzed int
	// Outstanding is whether a queue row resolving to it is anything but done.
	Outstanding bool
}

// LiveCommitEvidence reads which edges runs answer for each commit a queue row resolves to.
func (s *Store) LiveCommitEvidence() ([]LiveCommitEvidence, error) {
	return queryAll(s, "reading the evidence for each live commit",
		func(r *sql.Rows) (LiveCommitEvidence, error) {
			var e LiveCommitEvidence
			return e, r.Scan(&e.Commit, &e.Outstanding, &e.Answers, &e.Content, &e.Analyzed)
		}, `
		WITH live AS (
			SELECT LOWER(owner || '/' || repo) AS repository, LOWER(resolved_sha) AS commit_sha,
			       MAX(status <> 'done') AS outstanding
			  FROM abom_frontier
			 WHERE COALESCE(resolved_sha, '') <> '' AND COALESCE(owner, '') <> ''
			 GROUP BY 1, 2
		),
		edges_run AS (
			SELECT LOWER(q.repository) AS repository, LOWER(q.db_commit) AS commit_sha,
			       MAX(q.sighted_content) AS content
			  FROM abom_query_run q
			 WHERE q.analysis_mode = 'regular' AND COALESCE(q.db_commit, '') <> ''
			   AND `+edgesRun("q")+`
			 GROUP BY 1, 2
		),
		analyzed AS (SELECT repository, COUNT(*) AS commits FROM edges_run GROUP BY 1),
		answer AS (
			SELECT l.repository, l.commit_sha, x.content FROM live l
			  JOIN edges_run x ON x.repository = l.repository AND x.commit_sha = l.commit_sha
			UNION ALL
			SELECT l.repository, l.commit_sha, x.content FROM live l
			  JOIN abom_commit_content a
			    ON LOWER(a.repository) = l.repository AND LOWER(a.commit_sha) = l.commit_sha
			  JOIN abom_commit_content b ON b.repository = a.repository
			   AND b.indexed_digest = a.indexed_digest AND b.commit_sha <> a.commit_sha
			  JOIN edges_run x ON x.repository = l.repository AND x.commit_sha = LOWER(b.commit_sha)
			 WHERE NOT EXISTS (SELECT 1 FROM edges_run y
			                    WHERE y.repository = l.repository AND y.commit_sha = l.commit_sha)
		)
		SELECT l.repository || '@' || l.commit_sha, l.outstanding, COUNT(a.content),
		       COALESCE(MAX(a.content), 0), COALESCE(an.commits, 0)
		  FROM live l
		  LEFT JOIN answer a ON a.repository = l.repository AND a.commit_sha = l.commit_sha
		  LEFT JOIN analyzed an ON an.repository = l.repository
		 GROUP BY l.repository, l.commit_sha
		 ORDER BY 1`)
}

// LinesInScope counts the lines whose declaring node is live, which Relationships must account for.
func (s *Store) LinesInScope() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_edge_regular e
		JOIN abom_node_live src ON src.row_id = e.src_node_row_id`).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting lines in scope: %w", err)
	}
	return n, nil
}
