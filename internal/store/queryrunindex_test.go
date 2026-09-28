package store

import (
	"strings"
	"testing"
)

// Every other index waits for CreateIndexes, which runs after bulk loading and never on a server
// that only reads. Suite provenance joins each queue row to the runs on its commit, so without
// this index the Discovery page scans the whole run table once per row.
func TestRunIdentityIsIndexedForTheProvenanceJoin(t *testing.T) {
	s := newTestStore(t)

	rows, err := s.DB().Query(`
		EXPLAIN QUERY PLAN
		SELECT (SELECT GROUP_CONCAT(r.pack) FROM abom_query_run r
		        WHERE r.repository = abom_frontier.owner || '/' || abom_frontier.repo
		          AND r.db_commit = abom_frontier.resolved_sha)
		FROM abom_frontier`)
	if err != nil {
		t.Fatalf("explaining the provenance join: %v", err)
	}
	defer rows.Close()

	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("reading the plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the plan: %v", err)
	}

	joined := strings.Join(plan, "; ")
	if !strings.Contains(joined, "idx_query_run_repo") {
		t.Errorf("the join does not use the run identity index: %s", joined)
	}
	if strings.Contains(joined, "SCAN r") {
		t.Errorf("the join scans the whole run table once per queue row: %s", joined)
	}
}
