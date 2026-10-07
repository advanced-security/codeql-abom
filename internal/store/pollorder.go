package store

import (
	"database/sql"
	"fmt"
	"time"
)

// PollCandidate is an analyzed reference to a repository outside the declared scope that can move.
type PollCandidate struct {
	NodeKey string
	// Ref is the reference as requested; empty is the default branch.
	Ref         string
	Owner       string
	Repo        string
	ResolvedSHA string
	// Reach counts starting points running the version at ResolvedSHA; nil is unmeasured, not zero.
	Reach *int
}

// pollable excludes commit pins as DueForRecheck does; the queue records no ref kind, so a version-shaped name orders as a tag.
var pollable = `
	SELECT abom_frontier.node_key, abom_frontier.ref, abom_frontier.owner, abom_frontier.repo,
	       abom_frontier.resolved_sha, abom_frontier.checked_at, abom_frontier.analyzed_at,
	       CASE WHEN b.node_row_id IS NULL THEN NULL ELSE (
	         SELECT COUNT(*) FROM abom_node_version_reach v
	           JOIN repository r ON r.row_id = v.repository_row_id
	          WHERE v.node_row_id = n.row_id AND v.commit_sha = LOWER(abom_frontier.resolved_sha)
	            AND r.origin = 'seed') END AS reach,
	       abom_frontier.ref GLOB 'v[0-9]*' OR abom_frontier.ref GLOB '[0-9]*'
	         OR abom_frontier.ref GLOB 'refs/tags/*' AS tag
	  FROM abom_frontier
	  LEFT JOIN abom_node_live n ON n.node_key = abom_frontier.node_key
	  LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
	 WHERE abom_frontier.status = 'done'
	   AND COALESCE(abom_frontier.resolved_sha, '') <> ''
	   AND (abom_frontier.analyzed_ref IS NULL OR abom_frontier.analyzed_ref <> abom_frontier.resolved_sha)
	   AND NOT (length(abom_frontier.ref) = 40 AND abom_frontier.ref NOT GLOB '*[^0-9a-fA-F]*')
	   AND COALESCE(abom_frontier.owner, '') <> '' AND COALESCE(abom_frontier.repo, '') <> ''
	   AND NOT ` + ownedFrontierRow

// PollDue lists references unread since a time: most reach first, unmeasured before zero, a branch before a tag.
func (s *Store) PollDue(since time.Time, limit int) ([]PollCandidate, error) {
	cutoff := since.UTC().Format(time.RFC3339Nano)
	return queryAll(s, "reading the references due a poll", func(r *sql.Rows) (PollCandidate, error) {
		var c PollCandidate
		var reach sql.NullInt64
		err := r.Scan(&c.NodeKey, &c.Ref, &c.Owner, &c.Repo, &c.ResolvedSHA, &reach)
		if reach.Valid {
			n := int(reach.Int64)
			c.Reach = &n
		}
		return c, err
	}, `SELECT node_key, ref, owner, repo, resolved_sha, reach FROM (`+pollable+`)
	     WHERE (checked_at IS NULL OR checked_at <= ?) AND (analyzed_at IS NULL OR analyzed_at <= ?)
	     ORDER BY CASE WHEN reach > 0 THEN 0 WHEN reach IS NULL THEN 1 ELSE 2 END, reach DESC,
	              tag, checked_at IS NOT NULL, checked_at, node_key, ref
	     LIMIT ?`, cutoff, cutoff, limit)
}

// PollReach counts references whose version that many starting points run.
type PollReach struct {
	Reach      int `json:"reach"`
	References int `json:"references"`
}

// PollWork is every reference polling cycles through, counted once per repository and reference.
type PollWork struct {
	References int `json:"references"`
	// ByReach is those with a reach above zero, most first; the rest are unreached or unmeasured.
	ByReach []PollReach `json:"byReach"`
}

// ReadPollWork counts the references polling cycles through inside bound, by the reach that
// orders them.
func (s *Store) ReadPollWork(bound QueueBound) (PollWork, error) {
	out := PollWork{ByReach: []PollReach{}}
	inside, args := bound.Admits("abom_frontier.")
	rows, err := s.db.Query(`
		SELECT reach, COUNT(*) FROM (
		  SELECT MAX(reach) AS reach FROM (`+pollable+` AND `+inside+`) GROUP BY owner, repo, ref)
		GROUP BY reach ORDER BY reach DESC`, args...)
	if err != nil {
		return out, fmt.Errorf("counting the references polling asks about: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var reach sql.NullInt64
		var n int
		if err := rows.Scan(&reach, &n); err != nil {
			return out, fmt.Errorf("counting the references polling asks about: %w", err)
		}
		out.References += n
		if reach.Valid && reach.Int64 > 0 {
			out.ByReach = append(out.ByReach, PollReach{Reach: int(reach.Int64), References: n})
		}
	}
	return out, rows.Err()
}
