package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// VersionFindings is what findings runs reported in a dependency's own code at one commit.
type VersionFindings struct {
	Count    int            `json:"count"`
	Severity SeverityCounts `json:"severity"`
}

// findingsRun is the condition that a run reports findings: a run of its query has sighted one.
// The store never learns a query's purpose, and a graph query sights none.
func findingsRun(run string) string {
	return run + `.query_id IN (SELECT DISTINCT fq.query_id FROM abom_finding_sighting fs
	                              JOIN abom_query_run fq ON fq.row_id = fs.query_run_id)`
}

// FindingsAtCommits says, for each commit of one dependency, what findings runs reported in its
// own code there — or at a commit whose indexed content is identical, which the crawl settled
// without analyzing again. A commit absent from the answer is one no findings run reached, which
// is not analyzed rather than clean.
//
// A run older than the first sighting on record is no evidence either way: its sightings, had it
// made any, were never written.
func (s *Store) FindingsAtCommits(key string, commits []string) (map[string]VersionFindings, error) {
	out := map[string]VersionFindings{}
	var repository string
	err := s.db.QueryRow(`SELECT LOWER(COALESCE(owner, '') || '/' || COALESCE(repo, ''))
		FROM abom_node_live WHERE node_key = ?`, key).Scan(&repository)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the repository of %q: %w", key, err)
	}
	for _, commit := range commits {
		if commit == "" {
			continue
		}
		found, ok, err := s.findingsAtCommit(key, repository, strings.ToLower(commit))
		if err != nil {
			return nil, err
		}
		if ok {
			out[commit] = found
		}
	}
	return out, nil
}

func (s *Store) findingsAtCommit(key, repository, commit string) (VersionFindings, bool, error) {
	var out VersionFindings
	ids, reached, err := s.findingIDsAtCommit(key, repository, commit)
	if err != nil || !reached {
		return out, false, err
	}
	list, args := idList(ids)
	if err := s.db.QueryRow(`SELECT COUNT(*), `+bandSums("security_severity")+`
		  FROM abom_finding_regular WHERE row_id IN `+list, args...).
		Scan(append([]any{&out.Count}, out.Severity.targets()...)...); err != nil {
		return out, false, fmt.Errorf("counting findings of %q at %s: %w", key, commit, err)
	}
	return out, true, nil
}

// findingIDsAtCommit lists the findings runs at a commit reported in one dependency's own code, and
// whether any findings run reached that commit at all.
func (s *Store) findingIDsAtCommit(key, repository, commit string) ([]int64, bool, error) {
	runs, err := s.runsAtCommit(repository, commit, `q.ran_at >= (SELECT MIN(fq.ran_at)
		FROM abom_finding_sighting fs JOIN abom_query_run fq ON fq.row_id = fs.query_run_id)
		AND `+findingsRun("q"))
	if err != nil || len(runs) == 0 {
		return nil, false, err
	}
	list, args := idList(runs)
	ids, err := queryAll(s, "reading findings at a commit", scanID, `
		SELECT DISTINCT f.row_id FROM abom_finding_regular f
		  JOIN abom_finding_node fn ON fn.finding_row_id = f.row_id
		  JOIN abom_node_live n ON n.row_id = fn.node_row_id AND n.node_key = ?
		  JOIN abom_finding_sighting sg ON sg.finding_row_id = f.row_id
		 WHERE sg.query_run_id IN `+list, append([]any{key}, args...)...)
	return ids, true, err
}

// runsAtCommit lists the regular runs `also` admits of a repository at a commit, or at one whose
// indexed content is identical, which the crawl settled without analyzing again.
//
// A findings run older than the first sighting on record is no evidence either way: its
// sightings, had it made any, were never written. That is the findings caller's `also`.
func (s *Store) runsAtCommit(repository, commit, also string) ([]int64, error) {
	return queryAll(s, "reading runs at a commit", scanID, `
		WITH answering(commit_sha) AS (
			SELECT ?2
			UNION
			SELECT LOWER(b.commit_sha) FROM abom_commit_content a
			  JOIN abom_commit_content b
			    ON b.repository = a.repository AND b.indexed_digest = a.indexed_digest
			 WHERE LOWER(a.repository) = ?1 AND LOWER(a.commit_sha) = ?2)
		SELECT q.row_id FROM abom_query_run q
		 WHERE q.analysis_mode = 'regular' AND LOWER(q.repository) = ?1
		   AND LOWER(q.db_commit) IN (SELECT commit_sha FROM answering) AND `+also,
		repository, commit)
}

func scanID(r *sql.Rows) (int64, error) {
	var id int64
	return id, r.Scan(&id)
}

// idList renders row ids as an IN list; an empty one matches nothing.
func idList(ids []int64) (string, []any) {
	if len(ids) == 0 {
		return "(NULL)", nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(" + placeholders(len(ids)) + ")", args
}

// Queue states as a reader reads them, for one dependency's references.
const (
	QueueAnalyzed    = "analyzed"
	QueueQueued      = "queued"
	QueueRetrying    = "retrying"
	QueueWaiting     = "waiting"
	QueueRunning     = "running"
	QueueUnavailable = "unavailable"
)

// QueueStatesOf says where each queued reference of a node stands. A reference absent from the
// answer was never queued, which is not the same as queued and waiting.
func (s *Store) QueueStatesOf(key string) (map[string]string, error) {
	out := map[string]string{}
	rows, err := s.db.Query(`SELECT COALESCE(ref, ''), status, COALESCE(attempts, 0),
		awaiting_code_scanning IS NOT NULL FROM abom_frontier WHERE node_key = ?`, key)
	if err != nil {
		return nil, fmt.Errorf("reading the queue for %q: %w", key, err)
	}
	defer rows.Close()
	for rows.Next() {
		var ref, status string
		var attempts int
		var held bool
		if err := rows.Scan(&ref, &status, &attempts, &held); err != nil {
			return nil, fmt.Errorf("scanning a queue row: %w", err)
		}
		switch {
		case status == StatusDone:
			out[ref] = QueueAnalyzed
		case status == StatusPending && held:
			out[ref] = QueueWaiting
		case status == StatusPending && attempts > 0:
			out[ref] = QueueRetrying
		case status == StatusPending:
			out[ref] = QueueQueued
		default:
			out[ref] = status
		}
	}
	return out, rows.Err()
}

// AdvisoriesNaming lists the live advisories whose package is this node's repository, each with
// its ranges verbatim. Whether a reference falls inside one is not decided here.
func (s *Store) AdvisoriesNaming(key string) ([]AdvisoryReach, error) {
	rows, err := s.db.Query(`
		SELECT a.ghsa_id, COALESCE(a.cve_id,''), COALESCE(a.severity,''),
		       COALESCE(a.summary,''), COALESCE(a.html_url,''),
		       a.cvss_v3_score, a.epss_percentage,
		       p.package_name, p.vulnerable_version_range,
		       COALESCE(p.first_patched_version,''),
		       n.node_key, COALESCE(b.dependent_repos, 0)
		FROM abom_advisory a
		JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		JOIN abom_node_live n
		  ON n.owner IS NOT NULL AND n.repo IS NOT NULL
		 AND n.owner || '/' || n.repo = p.package_name
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		WHERE a.withdrawn_at IS NULL AND n.node_key = ?
		ORDER BY a.ghsa_id, p.package_name`, key)
	if err != nil {
		return nil, fmt.Errorf("reading advisories naming %q: %w", key, err)
	}
	defer rows.Close()
	out, err := scanAdvisoryReach(rows)
	if err != nil {
		return nil, err
	}
	sortAdvisoryReach(out)
	return out, nil
}

// RepositoriesInScope names, lowercased, the repositories a scope bounds a question to: the owned
// declaration, the property terms, or both. Nil when neither was asked, which is unbounded rather
// than empty; declaring nothing and asking for what is owned is empty.
func (s *Store) RepositoriesInScope(owned bool, sc RepositoryScope) (map[string]bool, error) {
	if !owned && !sc.asked() {
		return nil, nil
	}
	bound, args, err := s.restrict(sc, "r.repository_full_name")
	if err != nil {
		return nil, err
	}
	where := `WHERE 1` + bound
	if owned {
		where += ` AND ` + ownedFullName("r.repository_full_name")
	}
	rows, err := s.db.Query(`SELECT LOWER(r.repository_full_name) FROM repository r `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("reading the repositories in scope: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scanning a repository in scope: %w", err)
		}
		out[name] = true
	}
	return out, rows.Err()
}
