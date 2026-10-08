package store

import "testing"

// An owned repository used as an action by another is in that repository's supply chain at the
// reference that uses it. Its node is the owned repository's own — internal, with no owner, its
// repo the full name — so a closure rooted only at external nodes stopped at it, while reach walked
// into it: check 8 of the consistency checker found the two disagreeing on the crawled organization.
func TestAnOwnedRepositoryUsedAsAnActionIsInTheClosureAtTheReferenceThatUsesIt(t *testing.T) {
	s := newTestStore(t)
	insert := func(query string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := s.DB().QueryRow(query+" RETURNING row_id", args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return id
	}
	consumer := insert(`INSERT INTO repository (repository_full_name, origin) VALUES ('octo/app','seed')`)
	owned := insert(`INSERT INTO repository (repository_full_name, origin) VALUES ('octo/action','seed')`)
	node := func(key, kind, owner, repo string, external int, repository any) int64 {
		return insert(`INSERT INTO abom_node (node_key, node_kind, owner, repo, is_external, repository_row_id)
			VALUES (?,?,NULLIF(?,''),NULLIF(?,''),?,?)`, key, kind, owner, repo, external, repository)
	}
	ciStep := node("step:octo/app:.github/workflows/ci.yml:build:0", "step", "", "", 0, consumer)
	action := node("octo/action", "composite_action", "", "octo/action", 0, owned)
	actionStep := node("step:octo/action:action.yml::0", "step", "", "", 0, owned)
	cache := node("actions/cache", "action", "actions", "cache", 1, nil)
	if _, err := s.DB().Exec(`UPDATE abom_node SET container_node_row_id = ? WHERE row_id = ?`,
		action, actionStep); err != nil {
		t.Fatal(err)
	}
	run := insert(`INSERT INTO abom_query_run (run_key, query_id, repository, db_commit, ran_at)
		VALUES ('r-c1', 'abom/edges', 'octo/action', 'cccccccccc', '2026-01-01T00:00:00Z')`)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_edge (edge_key, src_node_row_id, dst_node_row_id, edge_kind, ref_raw,
		                       ref_value, file_path, repository_row_id, query_run_id, start_line)
		VALUES ('e1', ?, ?, 'step-uses-action', 'octo/action@v1', 'v1', '.github/workflows/ci.yml', ?, NULL, 1),
		       ('e2', ?, ?, 'composite-step-uses-action', 'actions/cache@v4', 'v4', 'action.yml', ?, ?, 1)`,
		ciStep, action, consumer, actionStep, cache, owned, run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at, resolved_sha)
		VALUES ('octo/action','v1','octo','action',1,'done','2026-01-01T00:00:00Z','cccccccccc')`); err != nil {
		t.Fatal(err)
	}

	got, err := s.SubmissionClosure("octo/app")
	if err != nil {
		t.Fatalf("SubmissionClosure: %v", err)
	}
	found := map[string]string{}
	for _, e := range got {
		found[e.Owner+"/"+e.Repo+"@"+e.Ref] = e.Relationship
	}
	if found["octo/action@v1"] != "direct" || found["actions/cache@v4"] != "indirect" {
		t.Errorf("closure %+v: want octo/action@v1 direct and, through it, actions/cache@v4 indirect", got)
	}
}
