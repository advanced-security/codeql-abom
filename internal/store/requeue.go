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
// have just fixed something they own. Naming a repository that could not be fetched retries it,
// since fixing access to one repository is exactly that kind of fix.
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
	if err != nil {
		return 0, err
	}
	retried, err := s.retryUnavailable(` AND LOWER(owner || '/' || repo) = LOWER(?)`, fullName)
	if err != nil {
		return 0, err
	}
	return int(n) + retried.Requeued, nil
}

// UnavailableRetry is what an operator's retry of unavailable work did.
type UnavailableRetry struct {
	Requeued int `json:"requeued"`
	// Left unavailable, because no change of access can fetch a reference that names no
	// repository.
	NamesNoRepository int `json:"namesNoRepository"`
}

// RetryUnavailable puts every dependency that could not be fetched back in the queue.
//
// Nothing retries unavailable work on its own: Git reports a missing credential and a missing
// repository alike, so only an operator knows that access changed.
func (s *Store) RetryUnavailable() (UnavailableRetry, error) {
	return s.retryUnavailable("")
}

// retryUnavailable resets the unavailable rows a filter selects to a fresh pending state.
//
// The rows are read in full before any is written: the store has one connection, and a write
// issued while a result set is still open waits on it forever.
func (s *Store) retryUnavailable(filter string, args ...any) (UnavailableRetry, error) {
	type row struct{ key, ref, owner, repo string }
	rows, err := queryAll(s, "reading unavailable work", func(r *sql.Rows) (row, error) {
		var it row
		return it, r.Scan(&it.key, &it.ref, &it.owner, &it.repo)
	}, `SELECT node_key, ref, COALESCE(owner, ''), COALESCE(repo, '') FROM abom_frontier
		WHERE status = 'unavailable'`+filter, args...)
	if err != nil {
		return UnavailableRetry{}, err
	}

	var out UnavailableRetry
	tx, err := s.db.Begin()
	if err != nil {
		return out, fmt.Errorf("retrying unavailable work: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, it := range rows {
		if !NamesARepository(it.owner, it.repo) {
			out.NamesNoRepository++
			continue
		}
		res, err := tx.Exec(`
			UPDATE abom_frontier
			SET status = 'pending', attempts = 0, next_attempt_at = NULL, claimed_at = NULL,
			    completed_at = NULL, reason = NULL
			WHERE node_key = ? AND ref = ? AND status = 'unavailable'`, it.key, it.ref)
		if err != nil {
			return UnavailableRetry{}, fmt.Errorf("retrying %s: %w", it.key, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return UnavailableRetry{}, err
		}
		out.Requeued += int(n)
	}
	if err := tx.Commit(); err != nil {
		return UnavailableRetry{}, fmt.Errorf("retrying unavailable work: %w", err)
	}
	return out, nil
}

// NamesARepository reports whether owner/repo could address a real repository.
//
// A `uses:` assembled from an expression extracts with the expression where the account should
// be, giving keys like `${{ github.repository }}/setup`. Recording the reference verbatim is
// right — it is what the workflow says — but no such repository exists, so a checkout only
// rediscovers that.
func NamesARepository(owner, repo string) bool {
	if owner == "" || repo == "" || len(owner) > 39 {
		return false
	}
	for _, r := range owner {
		if !isASCIILetterOrDigit(r) && r != '-' {
			return false
		}
	}
	for _, r := range repo {
		if !isASCIILetterOrDigit(r) && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func isASCIILetterOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
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
