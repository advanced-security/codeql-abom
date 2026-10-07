package graph

import (
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Commits of vendor/action: what its v1 tag names today, and the older one a lock selected. Each
// ships a different child, which is the whole question: a consumer runs the child of the commit
// it runs, never the tag's.
const (
	vendorLive   = "1111111111111111111111111111111111111111"
	vendorLocked = "2222222222222222222222222222222222222222"
	olderLocked  = "3333333333333333333333333333333333333333"
	lockedRoot   = "4444444444444444444444444444444444444444"
	unlockedRoot = "5555555555555555555555555555555555555555"
)

// lockedVendorFixture has two repositories naming vendor/action@v1 from their workflow. One locks
// it to vendorLocked, whose composite step names older/tool@v1; the other runs the tag, whose
// composite step names newer/tool@v2. An advisory covers each tool.
func lockedVendorFixture(t *testing.T) *store.Store {
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

	repo := map[string]int64{}
	for name, origin := range map[string]string{
		"octo/locked": "seed", "octo/unlocked": "seed", "vendor/action": "upstream",
	} {
		repo[name] = id(`INSERT INTO repository (repository_full_name, origin) VALUES (?, ?)`,
			name, origin)
	}
	node := func(key, kind string, repository int64, external bool, container int64, owner, name string) int64 {
		t.Helper()
		var repoArg, containerArg any
		if repository != 0 {
			repoArg = repository
		}
		if container != 0 {
			containerArg = container
		}
		var ownerArg, nameArg any
		if owner != "" {
			ownerArg, nameArg = owner, name
		}
		return id(`INSERT INTO abom_node (node_key, node_kind, owner, repo, is_external,
		                                  repository_row_id, container_node_row_id)
		           VALUES (?, ?, ?, ?, ?, ?, ?)`, key, kind, ownerArg, nameArg, external,
			repoArg, containerArg)
	}
	vendor := node("vendor/action", "composite_action", repo["vendor/action"], true, 0, "vendor", "action")
	older := node("older/tool", "action", 0, true, 0, "older", "tool")
	newer := node("newer/tool", "action", 0, true, 0, "newer", "tool")

	run := func(repository, commit string) int64 {
		t.Helper()
		return id(`INSERT INTO abom_query_run (run_key, query_id, repository, db_commit, ran_at)
		           VALUES (?, 'abom/edges', ?, ?, '2026-09-27T00:00:00Z')`,
			repository+"@"+commit, repository, commit)
	}
	edge := func(key string, src, dst, repository, queryRun int64, kind, file, ref string) {
		t.Helper()
		edgeID := id(`INSERT INTO abom_edge (edge_key, query_run_id, src_node_row_id, dst_node_row_id,
		                                     edge_kind, ref_raw, ref_value, file_path, start_line,
		                                     repository_row_id)
		              VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
			key, queryRun, src, dst, kind, ref, ref, file, repository)
		exec(`INSERT INTO abom_edge_sighting (edge_row_id, query_run_id) VALUES (?, ?)`,
			edgeID, queryRun)
	}

	for name, commit := range map[string]string{"octo/locked": lockedRoot, "octo/unlocked": unlockedRoot} {
		workflow := node(name+"/.github/workflows/ci.yml", "workflow", repo[name], false, 0, "", "")
		step := node("step:"+name+":.github/workflows/ci.yml:build:0", "step", repo[name], false,
			workflow, "", "")
		edge(name+":ci:1", step, vendor, repo[name], run(name, commit), "step-uses-action",
			".github/workflows/ci.yml", "v1")
	}
	olderStep := node("step:vendor/action:action.yml::0", "step", repo["vendor/action"], false, vendor, "", "")
	newerStep := node("step:vendor/action:action.yml::1", "step", repo["vendor/action"], false, vendor, "", "")
	edge("vendor/action:older", olderStep, older, repo["vendor/action"],
		run("vendor/action", vendorLocked), "composite-step-uses-action", "action.yml", "v1")
	edge("vendor/action:newer", newerStep, newer, repo["vendor/action"],
		run("vendor/action", vendorLive), "composite-step-uses-action", "action.yml", "v2")

	// A starting point's own default branch is queued at depth 0, as the crawl roots it.
	for _, f := range []struct {
		key, ref, owner, name, resolved string
		depth                           int
	}{
		{"octo/locked", "", "octo", "locked", lockedRoot, 0},
		{"octo/unlocked", "", "octo", "unlocked", unlockedRoot, 0},
		{"vendor/action", "v1", "vendor", "action", vendorLive, 1},
		{"vendor/action", vendorLocked, "vendor", "action", vendorLocked, 1},
	} {
		exec(`INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
		                                 resolved_sha, analyzed_ref)
		      VALUES (?, ?, ?, ?, ?, 'done', '2026-09-27T00:00:00Z', ?, ?)`,
			f.key, f.ref, f.owner, f.name, f.depth, f.resolved, f.ref)
	}

	if err := s.RecordLockReading("octo/locked", lockedRoot, "test-reader",
		[]store.LockCoverage{{Coverage: "covered", Schema: "v0.0.2"},
			{WorkflowPath: ".github/workflows/ci.yml", Coverage: "covered", Schema: "v0.0.2"}},
		store.LockGraph{
			Dependencies: []store.LockDependency{
				{Pin: "vendor/action@v1", Owner: "vendor", Repo: "action", Ref: "v1",
					Algorithm: "sha1", Digest: vendorLocked},
				{Pin: "older/tool@v1", Owner: "older", Repo: "tool", Ref: "v1",
					Algorithm: "sha1", Digest: olderLocked},
			},
			Uses: []store.LockUse{{Parent: "vendor/action@v1", Child: "older/tool@v1"}},
			WorkflowPins: []store.LockWorkflowPin{
				{WorkflowPath: ".github/workflows/ci.yml", Pin: "vendor/action@v1"}},
		}); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}

	for _, pkg := range []string{"older/tool", "newer/tool"} {
		adv := id(`INSERT INTO abom_advisory (ghsa_id, severity, summary, fetched_at)
		           VALUES (?, 'high', ?, '2026-09-27T00:00:00Z')`, "GHSA-"+pkg, "covering "+pkg)
		exec(`INSERT INTO abom_advisory_package (advisory_row_id, ecosystem, package_name,
		                                         vulnerable_version_range)
		      VALUES (?, 'actions', ?, '< 99')`, adv, pkg)
	}
	return s
}

// A locked consumer runs the child of the commit its lock selects, and a consumer of the tag runs
// the tag's. Taking the child from the tag's analysis for both told the locked one to act on a
// dependency it never runs, and told it nothing about the one it does.
func TestTheSubmissionClosureFollowsTheCommitALockSelects(t *testing.T) {
	s := lockedVendorFixture(t)

	locked := submittedFrom(t, s, "octo/locked")
	if !locked["older/tool"] || locked["newer/tool"] {
		t.Errorf("octo/locked carries %v, want older/tool and not newer/tool", locked)
	}
	unlocked := submittedFrom(t, s, "octo/unlocked")
	if !unlocked["newer/tool"] || unlocked["older/tool"] {
		t.Errorf("octo/unlocked carries %v, want newer/tool and not older/tool", unlocked)
	}

	entries, err := s.SubmissionClosure("octo/locked")
	if err != nil {
		t.Fatalf("SubmissionClosure: %v", err)
	}
	for _, e := range entries {
		want := map[string]string{"vendor/action": vendorLocked, "older/tool": olderLocked}[e.Owner+"/"+e.Repo]
		if e.LockedCommit != want {
			t.Errorf("%s/%s locked at %q, want %q", e.Owner, e.Repo, e.LockedCommit, want)
		}
		if e.Ref != map[string]string{"vendor/action": "v1", "older/tool": "v1"}[e.Owner+"/"+e.Repo] {
			t.Errorf("%s/%s carries ref %q; the declared ref stays as written", e.Owner, e.Repo, e.Ref)
		}
	}
}

// The exposure walk is the same claim from the other end, and must agree: each consumer is exposed
// through the child of the commit it runs.
func TestExposureFollowsTheCommitALockSelects(t *testing.T) {
	s := lockedVendorFixture(t)

	locked := exposedFrom(t, s, "octo/locked")
	if hops, ok := locked["older/tool"]; !ok || hops != 2 {
		t.Errorf("octo/locked exposure %v, want older/tool at 2 hops", locked)
	}
	if _, ok := locked["newer/tool"]; ok {
		t.Errorf("octo/locked is exposed to newer/tool, which only the tag's commit ships")
	}
	unlocked := exposedFrom(t, s, "octo/unlocked")
	if hops, ok := unlocked["newer/tool"]; !ok || hops != 2 {
		t.Errorf("octo/unlocked exposure %v, want newer/tool at 2 hops", unlocked)
	}
	if _, ok := unlocked["older/tool"]; ok {
		t.Errorf("octo/unlocked is exposed to older/tool, which only the locked commit ships")
	}
}

const (
	midLocked = "6666666666666666666666666666666666666666"
	midLive   = "7777777777777777777777777777777777777777"
)

// deepenLockedVendor adds a level beneath vendor/action: at the locked commit its composite also
// calls mid/composite@v1, which octo/locked's lock selects at midLocked, where it runs leaf/tool.
// What the tag names for mid/composite runs other/leaf instead. octo/pinned names vendor/action by
// the locked commit directly, with no lock of its own.
func deepenLockedVendor(t *testing.T, s *store.Store) {
	t.Helper()
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
	lookup := func(query string, args ...any) int64 {
		t.Helper()
		var out int64
		if err := s.DB().QueryRow(query, args...).Scan(&out); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return out
	}
	vendorRepo := lookup(`SELECT row_id FROM repository WHERE repository_full_name = 'vendor/action'`)
	vendor := lookup(`SELECT row_id FROM abom_node WHERE node_key = 'vendor/action'`)
	vendorLockedRun := lookup(`SELECT row_id FROM abom_query_run WHERE db_commit = ?`, vendorLocked)

	midRepo := id(`INSERT INTO repository (repository_full_name, origin) VALUES ('mid/composite', 'upstream')`)
	pinnedRepo := id(`INSERT INTO repository (repository_full_name, origin) VALUES ('octo/pinned', 'seed')`)
	node := func(key, kind string, repository int64, external bool, container int64, owner, name string) int64 {
		t.Helper()
		var containerArg, ownerArg, nameArg, repoArg any
		if container != 0 {
			containerArg = container
		}
		if repository != 0 {
			repoArg = repository
		}
		if owner != "" {
			ownerArg, nameArg = owner, name
		}
		return id(`INSERT INTO abom_node (node_key, node_kind, owner, repo, is_external,
		                                  repository_row_id, container_node_row_id)
		           VALUES (?, ?, ?, ?, ?, ?, ?)`, key, kind, ownerArg, nameArg, external, repoArg,
			containerArg)
	}
	mid := node("mid/composite", "composite_action", midRepo, true, 0, "mid", "composite")
	leaf := node("leaf/tool", "action", 0, true, 0, "leaf", "tool")
	other := node("other/leaf", "action", 0, true, 0, "other", "leaf")
	run := func(repository, commit string) int64 {
		t.Helper()
		return id(`INSERT INTO abom_query_run (run_key, query_id, repository, db_commit, ran_at)
		           VALUES (?, 'abom/edges', ?, ?, '2026-09-27T00:00:00Z')`,
			repository+"@"+commit, repository, commit)
	}
	edge := func(key string, src, dst, repository, queryRun int64, kind, file, ref string) {
		t.Helper()
		edgeID := id(`INSERT INTO abom_edge (edge_key, query_run_id, src_node_row_id, dst_node_row_id,
		                                     edge_kind, ref_raw, ref_value, file_path, start_line,
		                                     repository_row_id)
		              VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
			key, queryRun, src, dst, kind, ref, ref, file, repository)
		exec(`INSERT INTO abom_edge_sighting (edge_row_id, query_run_id) VALUES (?, ?)`,
			edgeID, queryRun)
	}
	vendorMidStep := node("step:vendor/action:action.yml::2", "step", vendorRepo, false, vendor, "", "")
	edge("vendor/action:mid", vendorMidStep, mid, vendorRepo, vendorLockedRun,
		"composite-step-uses-action", "action.yml", "v1")
	leafStep := node("step:mid/composite:action.yml::0", "step", midRepo, false, mid, "", "")
	otherStep := node("step:mid/composite:action.yml::1", "step", midRepo, false, mid, "", "")
	edge("mid/composite:leaf", leafStep, leaf, midRepo, run("mid/composite", midLocked),
		"composite-step-uses-action", "action.yml", "v1")
	edge("mid/composite:other", otherStep, other, midRepo, run("mid/composite", midLive),
		"composite-step-uses-action", "action.yml", "v1")

	workflow := node("octo/pinned/.github/workflows/ci.yml", "workflow", pinnedRepo, false, 0, "", "")
	step := node("step:octo/pinned:.github/workflows/ci.yml:build:0", "step", pinnedRepo, false,
		workflow, "", "")
	edge("octo/pinned:ci:1", step, vendor, pinnedRepo, run("octo/pinned", "8888888888888888888888888888888888888888"),
		"step-uses-action", ".github/workflows/ci.yml", vendorLocked)

	for _, f := range []struct {
		key, ref, owner, name, resolved string
		depth                           int
	}{
		{"octo/pinned", "", "octo", "pinned", "8888888888888888888888888888888888888888", 0},
		{"mid/composite", "v1", "mid", "composite", midLive, 2},
		{"mid/composite", midLocked, "mid", "composite", midLocked, 2},
	} {
		exec(`INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
		                                 resolved_sha, analyzed_ref)
		      VALUES (?, ?, ?, ?, ?, 'done', '2026-09-27T00:00:00Z', ?, ?)`,
			f.key, f.ref, f.owner, f.name, f.depth, f.resolved, f.ref)
	}

	graph, err := s.LockGraphAt("octo/locked", lockedRoot)
	if err != nil {
		t.Fatalf("LockGraphAt: %v", err)
	}
	graph.Dependencies = append(graph.Dependencies, store.LockDependency{
		Pin: "mid/composite@v1", Owner: "mid", Repo: "composite", Ref: "v1", Algorithm: "sha1",
		Digest: midLocked})
	graph.Uses = append(graph.Uses, store.LockUse{Parent: "vendor/action@v1", Child: "mid/composite@v1"})
	if err := s.RecordLockReading("octo/locked", lockedRoot, "test-reader",
		[]store.LockCoverage{{Coverage: "covered", Schema: "v0.0.2"},
			{WorkflowPath: ".github/workflows/ci.yml", Coverage: "covered", Schema: "v0.0.2"}},
		graph); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}
	for _, pkg := range []string{"leaf/tool", "other/leaf"} {
		adv := id(`INSERT INTO abom_advisory (ghsa_id, severity, summary, fetched_at)
		           VALUES (?, 'high', ?, '2026-09-27T00:00:00Z')`, "GHSA-"+pkg, "covering "+pkg)
		exec(`INSERT INTO abom_advisory_package (advisory_row_id, ecosystem, package_name,
		                                         vulnerable_version_range)
		      VALUES (?, 'actions', ?, '< 99')`, adv, pkg)
	}
}

// Three levels down the commit is the caller's lock's choice, and it is that caller's alone.
// octo/pinned runs vendor/action at the very commit octo/locked's lock selects, but it has no lock:
// its vendor/action resolves mid/composite@v1 by the tag, so it runs other/leaf, never leaf/tool.
func TestACallersLockBindsTheRouteItAdmitsToThatCaller(t *testing.T) {
	s := lockedVendorFixture(t)
	deepenLockedVendor(t, s)

	locked := exposedFrom(t, s, "octo/locked")
	if hops, ok := locked["leaf/tool"]; !ok || hops != 3 {
		t.Errorf("octo/locked exposure %v, want leaf/tool at 3 hops", locked)
	}
	if _, ok := locked["other/leaf"]; ok {
		t.Error("octo/locked is exposed to other/leaf, which only the tag's mid/composite ships")
	}
	pinned := exposedFrom(t, s, "octo/pinned")
	if _, ok := pinned["leaf/tool"]; ok {
		t.Error("octo/pinned is exposed to leaf/tool through octo/locked's lock, which is not its own")
	}
	if hops, ok := pinned["other/leaf"]; !ok || hops != 3 {
		t.Errorf("octo/pinned exposure %v, want other/leaf at 3 hops", pinned)
	}

	lockedClosure := submittedFrom(t, s, "octo/locked")
	pinnedClosure := submittedFrom(t, s, "octo/pinned")
	if !lockedClosure["leaf/tool"] || lockedClosure["other/leaf"] {
		t.Errorf("octo/locked carries %v, want leaf/tool and not other/leaf", lockedClosure)
	}
	if !pinnedClosure["other/leaf"] || pinnedClosure["leaf/tool"] {
		t.Errorf("octo/pinned carries %v, want other/leaf and not leaf/tool", pinnedClosure)
	}
}
