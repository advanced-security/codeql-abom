package store

import "testing"

// Builds the shape that makes the projection non-trivial:
//
//	consumer/app  .github/workflows/ci.yml  ->  vendor/action@v1   (direct)
//	vendor/action action.yml                 ->  helper/tool@v2     (indirect, runs in the job)
//	vendor/action .github/workflows/test.yml ->  vendor/ci-only@v9  (vendor's own CI, NOT ours)
func projectionFixture(t *testing.T, s *Store) {
	t.Helper()

	var consumer int64
	if err := s.DB().QueryRow(
		`INSERT INTO repository (repository_full_name, origin) VALUES ('consumer/app','seed')
		 RETURNING row_id`).Scan(&consumer); err != nil {
		t.Fatalf("seeding the consumer: %v", err)
	}
	var vendor int64
	if err := s.DB().QueryRow(
		`INSERT INTO repository (repository_full_name, origin) VALUES ('vendor/action','upstream')
		 RETURNING row_id`).Scan(&vendor); err != nil {
		t.Fatalf("seeding the vendor: %v", err)
	}

	node := func(key, kind, owner, repo, path string, external int) int64 {
		t.Helper()
		var id int64
		if err := s.DB().QueryRow(
			`INSERT INTO abom_node (node_key, node_kind, owner, repo, path, is_external)
			 VALUES (?,?,?,?,?,?) RETURNING row_id`,
			key, kind, owner, repo, path, external).Scan(&id); err != nil {
			t.Fatalf("inserting node %s: %v", key, err)
		}
		return id
	}
	edge := func(key string, src, dst int64, kind, ref, file string, repo int64) {
		t.Helper()
		if _, err := s.DB().Exec(`
			INSERT INTO abom_edge (edge_key, src_node_row_id, dst_node_row_id, edge_kind,
			                       ref_value, file_path, repository_row_id, start_line)
			VALUES (?,?,?,?,?,?,?,1)`, key, src, dst, kind, ref, file, repo); err != nil {
			t.Fatalf("inserting edge %s: %v", key, err)
		}
	}

	ciStep := node("step:consumer/app:.github/workflows/ci.yml:build:0", "step", "", "", "", 0)
	vendorAction := node("vendor/action", "action", "vendor", "action", "", 1)
	helper := node("helper/tool", "action", "helper", "tool", "", 1)
	newer := node("newer/tool", "action", "newer", "tool", "", 1)
	ciOnly := node("vendor/ci-only", "action", "vendor", "ci-only", "", 1)

	// The consumer's workflow reaches the vendor action.
	edge("e1", ciStep, vendorAction, "step-uses-action", "v1", ".github/workflows/ci.yml", consumer)

	// A step *inside* the vendor's action manifest. Its container is the action node, which is
	// how the traversal walks from a dependency into what that dependency runs.
	vendorStep := node("step:vendor/action:action.yml::0", "step", "", "", "", 0)
	if _, err := s.DB().Exec(
		`UPDATE abom_node SET container_node_row_id = ? WHERE row_id = ?`,
		vendorAction, vendorStep); err != nil {
		t.Fatalf("linking the composite step to its action: %v", err)
	}

	// The vendor action at two versions. v1 vendors helper/tool; v2 replaced it with newer/tool.
	// The consumer pins v1, so only helper/tool is in its supply chain.
	run := func(runKey, sha string) int64 {
		t.Helper()
		var id int64
		if err := s.DB().QueryRow(`
			INSERT INTO abom_query_run (run_key, query_id, repository, db_commit, ran_at)
			VALUES (?, 'abom/edges', 'vendor/action', ?, '2026-01-01T00:00:00Z')
			RETURNING row_id`, runKey, sha).Scan(&id); err != nil {
			t.Fatalf("inserting query run %s: %v", runKey, err)
		}
		return id
	}
	oldRun, newRun := run("r-v1", "aaaaaaaaaa"), run("r-v2", "bbbbbbbbbb")
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
		                           resolved_sha, analyzed_at)
		VALUES ('vendor/action','v1','vendor','action',1,'done','2026-01-01T00:00:00Z',
		        'aaaaaaaaaa','2026-01-01T00:00:00Z'),
		       ('vendor/action','v2','vendor','action',1,'done','2026-01-01T00:00:00Z',
		        'bbbbbbbbbb','2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("seeding the vendor's analyzed refs: %v", err)
	}

	edgeRun := func(key string, src, dst int64, kind, ref, file string, repo, qrun int64) {
		t.Helper()
		if _, err := s.DB().Exec(`
			INSERT INTO abom_edge (edge_key, src_node_row_id, dst_node_row_id, edge_kind,
			                       ref_value, file_path, repository_row_id, query_run_id, start_line)
			VALUES (?,?,?,?,?,?,?,?,1)`, key, src, dst, kind, ref, file, repo, qrun); err != nil {
			t.Fatalf("inserting edge %s: %v", key, err)
		}
	}
	edgeRun("e2", vendorStep, helper, "composite-step-uses-action", "v2", "action.yml", vendor, oldRun)
	edgeRun("e4", vendorStep, newer, "composite-step-uses-action", "v9", "action.yml", vendor, newRun)

	// The vendor's own CI. Attributed to the same repository, and not the consumer's concern.
	vendorCIStep := node("step:vendor/action:.github/workflows/test.yml:t:0", "step", "", "", "", 0)
	edge("e3", vendorCIStep, ciOnly, "step-uses-action", "v9", ".github/workflows/test.yml", vendor)
}

// A dependency's children belong to the version the consumer pinned, not to every version ABOM
// has analyzed. An unconstrained closure reports transitive dependencies the repository does not
// have.
func TestAChildBelongsToTheVersionOfTheParentThatWasPinned(t *testing.T) {
	s := newTestStore(t)
	projectionFixture(t, s)

	got, err := s.SubmissionClosure("consumer/app")
	if err != nil {
		t.Fatalf("SubmissionClosure: %v", err)
	}
	for _, e := range got {
		if e.Owner == "newer" {
			t.Fatalf("a child of an unpinned version of the parent reached the closure: %+v", e)
		}
	}
	if len(got) != 2 {
		t.Fatalf("closure has %d entries, want 2 (the pinned action and its own child): %+v",
			len(got), got)
	}
}

// A repository's own composite action is not a manifest to GitHub. Those edges never leave the
// repository, so a traversal that only walks outward from external dependencies misses them.
func TestARepositorysOwnCompositeActionIsATransitiveDependency(t *testing.T) {
	s := newTestStore(t)
	projectionFixture(t, s)

	var consumer int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = 'consumer/app'`).
		Scan(&consumer); err != nil {
		t.Fatalf("finding the consumer: %v", err)
	}
	var cached int64
	if err := s.DB().QueryRow(`
		INSERT INTO abom_node (node_key, node_kind, owner, repo, is_external)
		VALUES ('actions/cache','action','actions','cache',1) RETURNING row_id`).
		Scan(&cached); err != nil {
		t.Fatalf("inserting the cache action: %v", err)
	}
	var localStep int64
	if err := s.DB().QueryRow(`
		INSERT INTO abom_node (node_key, node_kind, is_external)
		VALUES ('step:consumer/app:.github/actions/build-app/action.yml::0','step',0)
		RETURNING row_id`).Scan(&localStep); err != nil {
		t.Fatalf("inserting the local composite step: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_edge (edge_key, src_node_row_id, dst_node_row_id, edge_kind,
		                       ref_value, file_path, repository_row_id, start_line)
		VALUES ('e6', ?, ?, 'composite-step-uses-action', 'v6.1.0',
		        '.github/actions/build-app/action.yml', ?, 1)`,
		localStep, cached, consumer); err != nil {
		t.Fatalf("inserting the local composite edge: %v", err)
	}

	got, err := s.SubmissionClosure("consumer/app")
	if err != nil {
		t.Fatalf("SubmissionClosure: %v", err)
	}

	for _, e := range got {
		if e.Repo == "cache" {
			if e.Relationship != "indirect" {
				t.Errorf("actions/cache relationship = %q, want indirect", e.Relationship)
			}
			if e.Ref != "v6.1.0" {
				t.Errorf("actions/cache ref = %q, want v6.1.0", e.Ref)
			}
			return
		}
	}
	t.Fatalf("the repository's own composite action contributed nothing: %+v", got)
}

// ABOM attributes an edge to the repository whose database produced it, so a dependency's rows
// include how that dependency tests itself. A consumer's closure must not include that upstream CI.
func TestAnUpstreamProjectsOwnCIIsNotTheConsumersSupplyChain(t *testing.T) {
	s := newTestStore(t)
	projectionFixture(t, s)

	got, err := s.SubmissionClosure("consumer/app")
	if err != nil {
		t.Fatalf("SubmissionClosure: %v", err)
	}

	for _, e := range got {
		if e.Owner == "vendor" && e.Repo == "ci-only" {
			t.Fatalf("the vendor's own CI dependency reached the consumer's closure: %+v", e)
		}
	}
	if len(got) != 2 {
		t.Fatalf("closure has %d entries, want 2 (the direct use and the composite child): %+v",
			len(got), got)
	}
}

// Direct and indirect are a depth axis, while the edge boundary is an ownership axis. Both entries
// below are cross-owner and only one is direct.
func TestRelationshipIsDepthFromTheManifestNotOwnership(t *testing.T) {
	s := newTestStore(t)
	projectionFixture(t, s)

	got, err := s.SubmissionClosure("consumer/app")
	if err != nil {
		t.Fatalf("SubmissionClosure: %v", err)
	}

	byRepo := map[string]ClosureEntry{}
	for _, e := range got {
		byRepo[e.Repo] = e
	}

	direct, ok := byRepo["action"]
	if !ok {
		t.Fatalf("the directly used action is missing: %+v", got)
	}
	if direct.Relationship != "direct" {
		t.Errorf("vendor/action relationship = %q, want direct", direct.Relationship)
	}
	if direct.Manifest != ".github/workflows/ci.yml" {
		t.Errorf("vendor/action manifest = %q, want the consumer's workflow", direct.Manifest)
	}

	indirect, ok := byRepo["tool"]
	if !ok {
		t.Fatalf("the composite child is missing: %+v", got)
	}
	if indirect.Relationship != "indirect" {
		t.Errorf("helper/tool relationship = %q, want indirect", indirect.Relationship)
	}
	// The child is reached through the consumer's workflow, so it belongs to that manifest even
	// though the reference is written in the vendor's action.yml.
	if indirect.Manifest != ".github/workflows/ci.yml" {
		t.Errorf("helper/tool manifest = %q, want the consumer's workflow", indirect.Manifest)
	}
	if indirect.ParentKey != "vendor/action" {
		t.Errorf("helper/tool parent = %q, want vendor/action", indirect.ParentKey)
	}
}

// The reference belongs to the edge, not the node: one action node carries every version anyone
// pinned. Collapsing to the node would submit one arbitrary version of a shared action.
func TestTheClosureCarriesTheReferenceFromTheEdge(t *testing.T) {
	s := newTestStore(t)
	projectionFixture(t, s)

	got, err := s.SubmissionClosure("consumer/app")
	if err != nil {
		t.Fatalf("SubmissionClosure: %v", err)
	}
	want := map[string]string{"action": "v1", "tool": "v2"}
	for _, e := range got {
		if want[e.Repo] != e.Ref {
			t.Errorf("%s/%s ref = %q, want %q", e.Owner, e.Repo, e.Ref, want[e.Repo])
		}
	}
}

// A repository nobody has analyzed has an empty closure, and an empty closure must never be
// submitted: it would replace GitHub's own reading of the workflow with nothing. Absent is not
// zero, so the caller has to be able to tell them apart.
func TestAnUnknownRepositoryHasAnEmptyClosureRatherThanAnError(t *testing.T) {
	s := newTestStore(t)
	projectionFixture(t, s)

	got, err := s.SubmissionClosure("nobody/unseen")
	if err != nil {
		t.Fatalf("SubmissionClosure: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("closure = %+v, want empty", got)
	}
}
