package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// requeueDone is what every selective requeue does to the rows it picks.
//
// Unavailable is left alone, and no attempt is charged: nothing failed, the work is being asked
// for again, and charging it would let a few re-runs exhaust a dependency's retry budget.
const requeueDone = `
	UPDATE abom_frontier
	SET status = 'pending', attempts = 0, next_attempt_at = NULL, claimed_at = NULL
	WHERE status = 'done'`

// RequeueStale requeues done items whose repository was not analyzed at the identity given.
//
// A whole-tier requeue is the blunt instrument: the skip makes an unchanged pair free to
// re-analyze, but the checkout in front of it is not, so re-running a tier still clones every
// repository in it. Selecting on the identity is what makes "re-analyze what has not run since
// this pack changed" cost only the repositories it names.
//
// The digests are matched as a set rather than one at a time because a plan draws on several
// packs, and a repository missing any one of them has work to do.
func (s *Store) RequeueStale(cliVersion string, packDigests []string) (int, error) {
	if cliVersion == "" || len(packDigests) == 0 {
		return 0, fmt.Errorf(
			"store: RequeueStale needs a CodeQL version and at least one pack digest; " +
				"an empty identity would requeue everything recorded")
	}

	args := make([]any, 0, len(packDigests)+2)
	args = append(args, cliVersion)
	for _, d := range packDigests {
		args = append(args, d)
	}
	args = append(args, len(packDigests))

	res, err := s.db.Exec(requeueDone+` AND (
			SELECT COUNT(DISTINCT r.pack_digest) FROM abom_query_run r
			WHERE r.repository = abom_frontier.owner || '/' || abom_frontier.repo
			  AND r.cli_version = ?
			  AND r.pack_digest IN (`+placeholders(len(packDigests))+`)
		) < ?`, args...)
	if err != nil {
		return 0, fmt.Errorf("requeueing what is out of date: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// RequeueRepository requeues the dependencies one repository answers.
//
// The tier a dependency happens to sit in is not the granularity anyone reasons about when they
// have just fixed something they own.
func (s *Store) RequeueRepository(fullName string) (int, error) {
	if fullName == "" {
		return 0, fmt.Errorf("store: RequeueRepository needs a repository")
	}
	res, err := s.db.Exec(
		requeueDone+` AND LOWER(owner || '/' || repo) = LOWER(?)`, fullName)
	if err != nil {
		return 0, fmt.Errorf("requeueing %s: %w", fullName, err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// FrontierKeysFor returns the queued items one repository answers.
//
// Requeueing and expediting are separate steps because they are separate decisions: putting work
// back is about what to run, running it next is about when.
func (s *Store) FrontierKeysFor(fullName string) ([]string, error) {
	return queryAll(s, "reading queued items for "+fullName, func(r *sql.Rows) (string, error) {
		var key string
		return key, r.Scan(&key)
	}, `SELECT node_key FROM abom_frontier
		WHERE status = 'pending' AND LOWER(owner || '/' || repo) = LOWER(?)
		ORDER BY node_key`, fullName)
}
