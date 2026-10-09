package frontier

import (
	"fmt"
	"strings"
	"time"
)

// AwaitCodeScanning holds a pending row until code scanning reports it analyzed commit, or until
// the ceiling, and reports whether the row was pending to hold. The ceiling is the row's next
// attempt, so a claim and the loop's wake already honor it.
func (q *Queue) AwaitCodeScanning(k Key, commit string, until time.Time) (bool, error) {
	res, err := q.Store.DB().Exec(`
		UPDATE abom_frontier SET awaiting_code_scanning = ?, next_attempt_at = ?
		WHERE node_key = ? AND ref = ? AND status = ?`,
		strings.ToLower(commit), until.UTC().Format(time.RFC3339Nano), k.NodeKey, k.Ref, StatusPending)
	if err != nil {
		return false, fmt.Errorf("holding %q for code scanning: %w", k.NodeKey, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CodeScanningAnalyzed releases a repository's rows held for code scanning's analysis of commit.
func (q *Queue) CodeScanningAnalyzed(repository, commit string) ([]Key, error) {
	return q.release(`AND awaiting_code_scanning = LOWER(?)`, repository, commit)
}

// ReleaseCodeScanningHolds releases every row of a repository held for code scanning.
func (q *Queue) ReleaseCodeScanningHolds(repository string) ([]Key, error) {
	return q.release(`AND awaiting_code_scanning IS NOT NULL`, repository)
}

func (q *Queue) release(which string, args ...any) ([]Key, error) {
	rows, err := q.Store.DB().Query(`
		UPDATE abom_frontier SET awaiting_code_scanning = NULL, next_attempt_at = NULL
		WHERE status = 'pending' AND LOWER(owner || '/' || repo) = LOWER(?) `+which+`
		RETURNING node_key, ref`, args...)
	if err != nil {
		return nil, fmt.Errorf("releasing what waited for code scanning in %v: %w", args[0], err)
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.NodeKey, &k.Ref); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
