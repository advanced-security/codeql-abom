package store

import (
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// requeueUnavailable puts an unavailable row back as work nothing has touched, forgetting why it was
// settled.
const requeueUnavailable = `status = 'pending', attempts = 0, next_attempt_at = NULL, claimed_at = NULL,
	completed_at = NULL, reason = NULL`

// EnumerationFailure is the last read of an account, or of one repository in it, that did not
// answer. It lasts until a read of the same subject does.
type EnumerationFailure struct {
	Subject  string    `json:"subject"`
	FailedAt time.Time `json:"failedAt"`
	Reason   string    `json:"reason"`
}

// RecordEnumerationFailure keeps why reading an account or a repository failed, replacing an older
// reason for the same subject.
func (s *Store) RecordEnumerationFailure(subject, reason string, at time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO abom_enumeration_failure (subject, failed_at, reason) VALUES (?, ?, ?)
		ON CONFLICT(subject) DO UPDATE SET failed_at = excluded.failed_at, reason = excluded.reason`,
		subject, at.UTC().Format(time.RFC3339Nano), reason)
	if err != nil {
		return fmt.Errorf("recording that %s could not be read: %w", subject, err)
	}
	return nil
}

// ClearEnumerationFailure forgets a failure once the same subject has been read.
func (s *Store) ClearEnumerationFailure(subject string) error {
	if _, err := s.db.Exec(`DELETE FROM abom_enumeration_failure WHERE LOWER(subject) = LOWER(?)`,
		subject); err != nil {
		return fmt.Errorf("clearing the failure recorded for %s: %w", subject, err)
	}
	return nil
}

// enumerationFailures lists an account's own failure first, then those of its repositories the
// declaration still covers: one it stopped covering is no longer the account's to read.
func (s *Store) enumerationFailures(owner string) ([]EnumerationFailure, error) {
	rows, err := s.db.Query(`
		SELECT f.subject, f.failed_at, f.reason FROM abom_enumeration_failure f
		 WHERE LOWER(f.subject) = LOWER(?)
		    OR (LOWER(SUBSTR(f.subject, 1, LENGTH(?) + 1)) = LOWER(?) || '/' AND `+declaredFullName("f.subject")+`)
		 ORDER BY CASE WHEN LOWER(f.subject) = LOWER(?) THEN 0 ELSE 1 END, LOWER(f.subject)`,
		owner, owner, owner, owner)
	if err != nil {
		return nil, fmt.Errorf("reading what could not be read in %s: %w", owner, err)
	}
	defer rows.Close()
	out := []EnumerationFailure{}
	for rows.Next() {
		var f EnumerationFailure
		var at sql.NullString
		if err := rows.Scan(&f.Subject, &at, &f.Reason); err != nil {
			return nil, err
		}
		f.FailedAt = parseMark(at)
		out = append(out, f)
	}
	return out, rows.Err()
}

// Resync is what putting chosen queue rows back did, counted by the state each was found in.
type Resync struct {
	// Requeued rows are pending again with a fresh retry budget: analyzed, read with nothing to
	// analyze, unavailable, or waiting out a retry.
	Requeued int `json:"requeued"`
	// Queued rows were already waiting their turn and are left as they are.
	Queued int `json:"queued"`
	// Waiting rows are held for code scanning's own analysis, which the hold exists to reuse.
	Waiting int `json:"waiting"`
	// Running rows are being analyzed now, and are left to finish.
	Running int `json:"running"`
	// NamesNoRepository counts unavailable rows left alone, because no access fixes a reference that
	// names no repository.
	NamesNoRepository int `json:"namesNoRepository"`
	// NotFound counts chosen rows the queue no longer holds.
	NotFound int `json:"notFound"`
	// Repositories names each repository a chosen row is in; Owned those the declared scope covers.
	Repositories []string `json:"repositories"`
	Owned        []string `json:"owned"`
}

// ResyncRows puts back exactly the queue rows somebody chose, each as its state allows.
//
// Every other way to put work back is wider than the fix that prompts it: a tier, every
// unavailable row, everything stale, or every reference of one repository. Nothing already known is
// cleared, and no attempt is charged, for the reasons RequeueTier gives.
func (s *Store) ResyncRows(refs []NodeRef) (Resync, error) {
	out := Resync{Repositories: []string{}, Owned: []string{}}
	chosen := slices.Clone(refs)
	slices.SortFunc(chosen, func(a, b NodeRef) int {
		return strings.Compare(a.NodeKey+"\x00"+a.Ref, b.NodeKey+"\x00"+b.Ref)
	})
	chosen = slices.Compact(chosen)
	if len(chosen) == 0 {
		return out, nil
	}

	type row struct {
		key, ref, owner, repo, status string
		attempts                      int
		held, owned                   bool
	}
	args := make([]any, 0, len(chosen)*2)
	for _, r := range chosen {
		args = append(args, r.NodeKey, r.Ref)
	}
	// Read in full before any write: the store has one connection.
	rows, err := queryAll(s, "reading the rows to resync", func(r *sql.Rows) (row, error) {
		var it row
		return it, r.Scan(&it.key, &it.ref, &it.owner, &it.repo, &it.status, &it.attempts, &it.held, &it.owned)
	}, `SELECT node_key, ref, COALESCE(owner, ''), COALESCE(repo, ''), status, attempts,
	           CASE WHEN awaiting_code_scanning IS NOT NULL THEN 1 ELSE 0 END,
	           CASE WHEN `+isOwned+` THEN 1 ELSE 0 END
	      FROM abom_frontier
	     WHERE (node_key, ref) IN (VALUES `+strings.TrimSuffix(strings.Repeat("(?,?),", len(chosen)), ",")+`)`,
		args...)
	if err != nil {
		return out, err
	}
	out.NotFound = len(chosen) - len(rows)

	tx, err := s.db.Begin()
	if err != nil {
		return out, fmt.Errorf("resyncing chosen rows: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	repositories, owned := map[string]bool{}, map[string]bool{}
	for _, it := range rows {
		named := NamesARepository(it.owner, it.repo)
		if named {
			repositories[it.owner+"/"+it.repo] = true
			if it.owned {
				owned[it.owner+"/"+it.repo] = true
			}
		}
		var reset string
		switch {
		case it.status == StatusDone || it.status == StatusNothing:
			reset = `status = 'pending', attempts = 0, next_attempt_at = NULL, claimed_at = NULL`
		case it.status == StatusUnavailable && !named:
			out.NamesNoRepository++
		case it.status == StatusUnavailable:
			reset = requeueUnavailable
		case it.status == StatusPending && it.held:
			out.Waiting++
		case it.status == StatusPending && it.attempts > 0:
			reset = `attempts = 0, next_attempt_at = NULL, reason = NULL`
		case it.status == StatusPending:
			out.Queued++
		case it.status == StatusRunning:
			out.Running++
		}
		if reset == "" {
			continue
		}
		// The status read above is checked again: a row a worker settled since is left as it is.
		res, err := tx.Exec(`UPDATE abom_frontier SET `+reset+`
			WHERE node_key = ? AND ref = ? AND status = ?`, it.key, it.ref, it.status)
		if err != nil {
			return out, fmt.Errorf("resyncing %s: %w", it.key, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return out, err
		}
		out.Requeued += int(n)
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("resyncing chosen rows: %w", err)
	}
	out.Repositories = slices.Sorted(maps.Keys(repositories))
	out.Owned = slices.Sorted(maps.Keys(owned))
	return out, nil
}

// UnavailableRow is one unavailable queue row, with what the store knows that bears on whether its
// answer still holds.
type UnavailableRow struct {
	NodeKey, Ref, Owner, Repo, Reason string
	Depth                             int
	// Tier is the row's tier; SettledAt when it was settled unavailable, empty when unrecorded.
	Tier, SettledAt string
}

// UnavailableRows lists every unavailable queue row with the reason it was settled for.
func (s *Store) UnavailableRows() ([]UnavailableRow, error) {
	return queryAll(s, "reading unavailable work", func(r *sql.Rows) (UnavailableRow, error) {
		var it UnavailableRow
		return it, r.Scan(&it.NodeKey, &it.Ref, &it.Owner, &it.Repo, &it.Reason, &it.Depth, &it.Tier, &it.SettledAt)
	}, `SELECT node_key, ref, COALESCE(owner, ''), COALESCE(repo, ''), COALESCE(reason, ''), depth,
	           `+tierOfRow+`, COALESCE(completed_at, '')
	      FROM abom_frontier WHERE status = 'unavailable' ORDER BY node_key, ref`)
}

// FrontierTally is how many queue rows of one tier are in one state.
type FrontierTally struct {
	Tier, State string
	Rows        int
}

// FrontierStuck is pending work that failed at least the attempt ceiling: still retried, no longer
// holding its depth open. Split from retrying only where the ceiling is known.
const FrontierStuck = "stuck"

// FrontierTallies counts the queue by tier and by the states a reader distinguishes, with work past
// stuckAfter attempts apart from retrying. Every tier and state is present, zero included.
func (s *Store) FrontierTallies(stuckAfter int) ([]FrontierTally, error) {
	counted := map[[2]string]int{}
	rows, err := queryAll(s, "tallying the queue", func(r *sql.Rows) (FrontierTally, error) {
		var it FrontierTally
		return it, r.Scan(&it.Tier, &it.State, &it.Rows)
	}, `SELECT `+tierOfRow+`,
	           CASE WHEN status = 'pending' AND awaiting_code_scanning IS NOT NULL THEN 'waiting'
	                WHEN status = 'pending' AND attempts >= ? THEN 'stuck'
	                WHEN status = 'pending' AND attempts > 0 THEN 'retrying'
	                WHEN status = 'pending' THEN 'queued'
	                ELSE status END,
	           COUNT(*)
	      FROM abom_frontier GROUP BY 1, 2`, stuckAfter)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		counted[[2]string{r.Tier, r.State}] = r.Rows
	}
	var out []FrontierTally
	for _, tier := range []string{TierOwned, TierDirect, TierTransitive} {
		for _, state := range []string{FrontierQueued, FrontierRetrying, FrontierStuck, FrontierWaiting,
			StatusRunning, StatusDone, StatusNothing, StatusUnavailable} {
			out = append(out, FrontierTally{Tier: tier, State: state, Rows: counted[[2]string{tier, state}]})
		}
	}
	return out, nil
}

// ReconsiderUnavailable puts back each unavailable row that back names, with a fresh budget and no
// reason: a row settled before a classification existed can hold an answer that is no longer
// terminal. The rows are read in full before any is written, because the store has one connection.
func (s *Store) ReconsiderUnavailable(back func(UnavailableRow) bool) (int, error) {
	rows, err := s.UnavailableRows()
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("reconsidering unavailable work: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	n := 0
	for _, it := range rows {
		if !back(it) {
			continue
		}
		res, err := tx.Exec(`UPDATE abom_frontier SET `+requeueUnavailable+`
			WHERE node_key = ? AND ref = ? AND status = 'unavailable'`, it.NodeKey, it.Ref)
		if err != nil {
			return 0, fmt.Errorf("putting back %s: %w", it.NodeKey, err)
		}
		changed, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		n += int(changed)
	}
	return n, tx.Commit()
}
