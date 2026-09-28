package graph

import (
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

const flowsCommit = "6666666666666666666666666666666666666666"

// reusableWorkflowFixture separates a called workflow's steps from the callee's own CI.
func reusableWorkflowFixture(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.DB().Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	id := func(query string, args ...any) int64 {
		t.Helper()
		var out int64
		if err := s.DB().QueryRow(query+" RETURNING row_id", args...).Scan(&out); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return out
	}
	app := id(`INSERT INTO repository (repository_full_name, origin) VALUES ('octo/app', 'seed')`)
	flows := id(`INSERT INTO repository (repository_full_name, origin) VALUES ('third/flows', 'upstream')`)
	run := func(repository, commit string) int64 {
		t.Helper()
		return id(`INSERT INTO abom_query_run (run_key, query_id, repository, db_commit, ran_at)
		           VALUES (?, 'abom/edges', ?, ?, '2026-09-28T00:00:00Z')`,
			repository+"@"+commit, repository, commit)
	}
	node := func(key, kind, owner, repo, path string, external bool, repository, container int64) int64 {
		t.Helper()
		var repoArg, containerArg, ownerArg, nameArg, pathArg any
		if repository != 0 {
			repoArg = repository
		}
		if container != 0 {
			containerArg = container
		}
		if owner != "" {
			ownerArg, nameArg, pathArg = owner, repo, path
		}
		return id(`INSERT INTO abom_node (node_key, node_kind, owner, repo, path, is_external,
		                                  repository_row_id, container_node_row_id)
		           VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, key, kind, ownerArg, nameArg, pathArg, external,
			repoArg, containerArg)
	}
	edge := func(key string, src, dst, repository, queryRun int64, kind, file, ref string) {
		t.Helper()
		edgeID := id(`INSERT INTO abom_edge (edge_key, query_run_id, src_node_row_id, dst_node_row_id,
		                                     edge_kind, ref_raw, ref_value, file_path, start_line,
		                                     repository_row_id)
		              VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
			key, queryRun, src, dst, kind, ref, ref, file, repository)
		exec(`INSERT INTO abom_edge_sighting (edge_row_id, query_run_id) VALUES (?, ?)`, edgeID, queryRun)
	}

	ci := node("octo/app/.github/workflows/ci.yml", "workflow", "", "", "", false, app, 0)
	job := node("job:octo/app:.github/workflows/ci.yml:call", "job", "", "", "", false, app, ci)
	build := node("third/flows/.github/workflows/build.yml", "reusable_workflow",
		"third", "flows", ".github/workflows/build.yml", true, 0, 0)
	edge("app:call", job, build, app, run("octo/app", lockedRoot), "job-uses-reusable-workflow",
		".github/workflows/ci.yml", "v1")

	shipped := run("third/flows", flowsCommit)
	script := node("actions/github-script", "action", "actions", "github-script", "", true, 0, 0)
	buildStep := node("step:third/flows:.github/workflows/build.yml:b:0", "step", "", "", "", false,
		flows, build)
	edge("flows:build", buildStep, script, flows, shipped, "step-uses-action",
		".github/workflows/build.yml", "v7")

	test := node("third/flows/.github/workflows/test.yml", "workflow", "", "", "", false, flows, 0)
	labeler := node("actions/labeler", "action", "actions", "labeler", "", true, 0, 0)
	testStep := node("step:third/flows:.github/workflows/test.yml:t:0", "step", "", "", "", false,
		flows, test)
	edge("flows:test", testStep, labeler, flows, shipped, "step-uses-action",
		".github/workflows/test.yml", "v5")

	exec(`INSERT INTO abom_frontier (node_key, ref, owner, repo, subpath, depth, status, enqueued_at,
	                                 resolved_sha, analyzed_ref)
	      VALUES ('third/flows/.github/workflows/build.yml', 'v1', 'third', 'flows',
	              '.github/workflows/build.yml', 1, 'done', '2026-09-28T00:00:00Z', ?, 'v1')`,
		flowsCommit)

	for _, pkg := range []string{"actions/github-script", "actions/labeler"} {
		adv := id(`INSERT INTO abom_advisory (ghsa_id, severity, summary, fetched_at)
		           VALUES (?, 'high', ?, '2026-09-28T00:00:00Z')`, "GHSA-"+pkg, "covering "+pkg)
		exec(`INSERT INTO abom_advisory_package (advisory_row_id, ecosystem, package_name,
		                                         vulnerable_version_range)
		      VALUES (?, 'actions', ?, '< 99')`, adv, pkg)
	}
	return s
}

// Called workflow steps are in the caller's supply chain; the callee's own CI is not.
func TestBothWalksFollowACalledReusableWorkflowIntoItsSteps(t *testing.T) {
	s := reusableWorkflowFixture(t)

	exposed := exposedFrom(t, s, "octo/app")
	if hops, ok := exposed["actions/github-script"]; !ok || hops != 2 {
		t.Errorf("exposure %v, want actions/github-script at 2 hops", exposed)
	}
	submitted := submittedFrom(t, s, "octo/app")
	if !submitted["actions/github-script"] {
		t.Errorf("submission closure %v misses actions/github-script", submitted)
	}
	if _, ok := exposed["actions/labeler"]; ok || submitted["actions/labeler"] {
		t.Errorf("third/flows' own CI leaked in: exposure %v, submission %v", exposed, submitted)
	}
}
