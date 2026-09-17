package frontier

import (
	"fmt"
	"strings"
	"time"
)

// NodeRef is one queued item: a dependency at one reference.
type NodeRef struct {
	NodeKey string
	Ref     string
}

// ExpediteRefs marks specific references to be claimed ahead of the ranking.
//
// Expedite takes node keys, which prioritizes every reference of an action the crawl has ever
// seen. That is right for an operator asking about a repository and wrong for work chosen because
// one particular tree is worth reading: a popular action has dozens of analyzed references, and
// prioritizing the ones nobody pinned spends a checkout and a database build per useless tree.
func (q *Queue) ExpediteRefs(refs []NodeRef) (int, error) {
	if len(refs) == 0 {
		return 0, nil
	}

	args := make([]any, 0, len(refs)*2+1)
	args = append(args, q.now().Format(time.RFC3339Nano))
	for _, r := range refs {
		args = append(args, r.NodeKey, r.Ref)
	}

	res, err := q.Store.DB().Exec(`
		UPDATE abom_frontier SET expedited_at = ?
		WHERE status = '`+StatusPending+`' AND (node_key, ref) IN (VALUES `+
		strings.TrimSuffix(strings.Repeat("(?,?),", len(refs)), ",")+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("expediting %d references: %w", len(refs), err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}
