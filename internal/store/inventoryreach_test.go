package store

import "testing"

// runOrHeldFixture is one starting point and one upstream project, measured by a rebuild before
// one more reference arrives.
func runOrHeldFixture(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	vendor, err := s.InsertUpstreamRepository("vendor/tool")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, 0)
	add := adder(t, w)
	uses := func(step, action string, repo int64, file string) {
		t.Helper()
		if err := w.AddEdge(Edge{Key: step + ">" + action, SrcKey: step, DstKey: action,
			Kind: EdgeKindStepUsesAction, RefRaw: action + "@v1", RefValue: "v1",
			RepositoryID: repo, FilePath: file, StartLine: len(step), StartColumn: 9}); err != nil {
			t.Fatal(err)
		}
	}
	flush := func() {
		t.Helper()
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	add(Node{Key: "octo/app/ci.yml", Kind: "workflow", RepositoryID: app})
	add(Node{Key: "vendor/tool/ci.yml", Kind: "workflow", RepositoryID: vendor})
	add(Node{Key: "octo/app/held", Kind: "composite_action", RepositoryID: app})
	add(Node{Key: "vendor/tool/unused", Kind: "composite_action", RepositoryID: vendor})
	for _, a := range []string{"actions/checkout", "vendor/lint"} {
		add(Node{Key: a, Kind: "action", IsExternal: true})
	}
	flush()
	add(Node{Key: "app-1", Kind: "step", RepositoryID: app, ContainerKey: "octo/app/ci.yml", JobID: "build"})
	add(Node{Key: "tool-1", Kind: "step", RepositoryID: vendor, ContainerKey: "vendor/tool/ci.yml", JobID: "test"})
	flush()
	uses("app-1", "actions/checkout", app, "ci.yml")
	uses("tool-1", "vendor/lint", vendor, "ci.yml")
	flush()

	// What the walk writes: checkout is run by the starting point, lint only by the upstream CI.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_node_reach (node_row_id, repository_row_id, hops, steps, jobs, workflows)
		SELECT row_id, ?, 1, 1, 1, 1 FROM abom_node WHERE node_key = 'actions/checkout'`, app); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_blast_radius (node_row_id, dependent_repos, dependent_steps, dependent_jobs,
			dependent_workflows, max_depth, refs_in_use)
		SELECT row_id, CASE node_key WHEN 'actions/checkout' THEN 1 ELSE 0 END,
		       CASE node_key WHEN 'actions/checkout' THEN 1 ELSE 0 END,
		       CASE node_key WHEN 'actions/checkout' THEN 1 ELSE 0 END,
		       CASE node_key WHEN 'actions/checkout' THEN 1 ELSE 0 END, 1, 1
		  FROM abom_node WHERE node_key IN ('actions/checkout', 'vendor/lint')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordGraphGeneration(GraphRebuild{ReachRebuilt: true}); err != nil {
		t.Fatal(err)
	}

	add(Node{Key: "late/new", Kind: "action", IsExternal: true})
	add(Node{Key: "app-2", Kind: "step", RepositoryID: app, ContainerKey: "octo/app/ci.yml", JobID: "build"})
	flush()
	uses("app-2", "late/new", app, "ci.yml")
	flush()
	return s
}

// The Actions unit is what the starting points run or hold. An action only an upstream project's
// own CI uses, or that nothing uses, is somebody else's to manage, so it is left out and counted;
// one that arrived after reach was measured is not known to be either, so it stays and says so.
func TestTheActionsInventoryListsWhatStartingPointsRunOrHold(t *testing.T) {
	s := runOrHeldFixture(t)

	page, err := s.ActionsInventoryPage(InventoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ActionSummary{}
	for _, a := range page.Items {
		got[a.NodeKey] = a
	}
	for _, key := range []string{"actions/checkout", "octo/app/held", "late/new"} {
		if _, ok := got[key]; !ok {
			t.Errorf("%s is missing; the starting point runs or holds it", key)
		}
	}
	for _, key := range []string{"vendor/lint", "vendor/tool/unused"} {
		if _, ok := got[key]; ok {
			t.Errorf("%s is listed; no starting point runs or holds it", key)
		}
	}
	if page.Total != len(page.Items) || page.Unlisted != 2 {
		t.Errorf("total %d of %d items with %d unlisted, want every item counted and 2 unlisted",
			page.Total, len(page.Items), page.Unlisted)
	}
	if r := got["octo/app/held"].DependentRepos; r == nil || *r != 0 {
		t.Errorf("held reach = %v, want a measured 0: the rebuild found nothing using it", r)
	}
	if r := got["octo/app/held"].DependentSteps; r == nil || *r != 0 {
		t.Errorf("held steps = %v, want a measured 0", r)
	}
	if r := got["late/new"].DependentRepos; r != nil {
		t.Errorf("late reach = %d, want unmeasured: it arrived after the rebuild", *r)
	}
}

// A row written before this build counted steps, jobs and versions holds them as NULL. That is not
// counted, and reading it as zero says nothing runs the action at a level the row never measured.
func TestALevelARowNeverCountedIsNotZero(t *testing.T) {
	s := runOrHeldFixture(t)
	if _, err := s.DB().Exec(`UPDATE abom_blast_radius
		SET dependent_steps = NULL, dependent_jobs = NULL, refs_in_use = NULL
		WHERE node_row_id = (SELECT row_id FROM abom_node WHERE node_key = 'actions/checkout')`); err != nil {
		t.Fatal(err)
	}

	page, err := s.ActionsInventoryPage(InventoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range page.Items {
		if a.NodeKey != "actions/checkout" {
			continue
		}
		if a.DependentSteps != nil || a.DependentJobs != nil || a.DistinctRefs != nil {
			t.Errorf("steps %d, jobs %d, versions %d; want none counted", value(a.DependentSteps),
				value(a.DependentJobs), value(a.DistinctRefs))
		}
		if value(a.DependentRepos) != 1 {
			t.Errorf("repositories %d, want the 1 the row did count", value(a.DependentRepos))
		}
		return
	}
	t.Fatal("checkout is not listed")
}
