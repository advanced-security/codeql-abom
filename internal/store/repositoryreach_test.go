package store

import "testing"

// Check 8 compares each repository's reach with its submission closure, which roots at an owned
// repository's action where another repository names it by reference. Reach of that node is read
// for the comparison too, and a repository's own local composite action, which the closure never
// lists as an entry, is not.
func TestReachReadsAnotherOwnedRepositorysActionButNotARepositorysOwn(t *testing.T) {
	s := newTestStore(t)
	insert := func(query string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := s.DB().QueryRow(query+" RETURNING row_id", args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return id
	}
	app := insert(`INSERT INTO repository (repository_full_name, origin) VALUES ('octo/app','seed')`)
	owned := insert(`INSERT INTO repository (repository_full_name, origin) VALUES ('octo/action','seed')`)
	node := func(key string, external int, repository any) int64 {
		return insert(`INSERT INTO abom_node (node_key, node_kind, is_external, repository_row_id)
			VALUES (?, 'composite_action', ?, ?)`, key, external, repository)
	}
	for _, n := range []int64{
		node("octo/action", 0, owned),
		node("octo/app/.github/actions/local", 0, app),
		node("actions/cache", 1, nil),
	} {
		if _, err := s.DB().Exec(`INSERT INTO abom_node_reach (node_row_id, repository_row_id, hops,
			steps, jobs, workflows) VALUES (?, ?, 1, 1, 1, 1)`, n, app); err != nil {
			t.Fatal(err)
		}
	}

	reach, _, err := s.RepositoryReach()
	if err != nil {
		t.Fatal(err)
	}
	got := reach["octo/app"]
	if !got["octo/action"] || !got["actions/cache"] || got["octo/app/.github/actions/local"] || len(got) != 2 {
		t.Errorf("reach of octo/app read as %v: want octo/action and actions/cache, not its own local action", got)
	}
}
