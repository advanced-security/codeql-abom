package frontier

import (
	"fmt"
	"strings"
	"time"
)

// Expedite marks queued items to be claimed ahead of the ranking, returning how many were marked.
//
// Score ordering is right for a crawl and wrong for a person: someone who has just fixed a
// dependency they own wants that one answer, and filing the request behind nine hundred
// better-scored items refuses it in all but name. Among expedited items the ranking still
// decides, because expediting everything is expediting nothing.
//
// Only queued items can be marked. Reporting how many were is the difference between "it is
// scheduled" and "nothing by that name is waiting". A push held for code scanning is no longer
// held: whoever asks by name wants the answer now, and a retry's backoff stays as it was.
func (q *Queue) Expedite(nodeKeys ...string) (int, error) {
	if len(nodeKeys) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(nodeKeys)+1)
	args = append(args, q.now().Format(time.RFC3339Nano))
	for _, key := range nodeKeys {
		args = append(args, key)
	}

	res, err := q.Store.DB().Exec(`
		UPDATE abom_frontier SET expedited_at = ?, awaiting_code_scanning = NULL,
		       next_attempt_at = CASE WHEN awaiting_code_scanning IS NULL THEN next_attempt_at END
		WHERE status = '`+StatusPending+`' AND node_key IN (`+
		strings.TrimSuffix(strings.Repeat("?,", len(nodeKeys)), ",")+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("expediting %d items: %w", len(nodeKeys), err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}
