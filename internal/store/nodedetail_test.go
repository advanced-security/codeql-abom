package store

import "testing"

// Anything reachable only through a dependency that was never expanded cannot appear in any walk,
// so the count of what is still pending travels with every answer about who uses something.
func TestUnanalyzedDependenciesCountsWhatTheCrawlHasNotFinished(t *testing.T) {
	s := subgraphFixture(t)
	before, err := s.UnanalyzedDependencies()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, depth, status, score, enqueued_at)
		VALUES ('some/unexpanded', 1, 'pending', 1.0, CURRENT_TIMESTAMP),
		       ('some/finished', 1, 'done', 1.0, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seeding frontier: %v", err)
	}
	after, err := s.UnanalyzedDependencies()
	if err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Errorf("unanalyzed went %d -> %d after one pending and one done row, want +1", before, after)
	}
}

// What a node uses is folded to the container a reader recognizes, and an unknown node is a fair
// question with an empty answer rather than a null one.
func TestANodesDetailListsWhatItUsesAndNothingForAnUnknownNode(t *testing.T) {
	s := subgraphFixture(t)
	if _, err := s.NodeDetailOf("nobody/nothing", nil); err == nil {
		t.Error("an unknown node answered as though it existed")
	}
	uses, err := s.nodeUses("nobody/nothing", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if uses == nil {
		t.Error("an unknown node's uses are nil; an empty list is the contract")
	}
}

// A workflow its triggers run is named by no uses: line; one another workflow calls is named by
// the line that calls it. A page reads which it is from this count, never from the node's kind:
// 147 of the crawled organization's 2,215 workflows are called by a local path.
func TestANodesDetailCountsTheUsesLinesNamingIt(t *testing.T) {
	s := subgraphFixture(t)
	for key, want := range map[string]int{
		"octo-org/app/.github/workflows/ci.yml":       0,
		"octo-org/shared/.github/workflows/build.yml": 1,
		"actions/checkout":                            4,
	} {
		d, err := s.NodeDetailOf(key, nil)
		if err != nil {
			t.Fatalf("NodeDetailOf(%s): %v", key, err)
		}
		if d.NamedBy != want {
			t.Errorf("%s is named by %d uses: lines, want %d", key, d.NamedBy, want)
		}
	}
}

// A job another job needs is ordered after it, not run by it: `needs:` is not a uses: line.
func TestAJobAnotherNeedsIsNamedByNoUsesLine(t *testing.T) {
	s := newTestStore(t)
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, 0)
	add := adder(t, w)
	add(Node{Key: "octo/app/ci.yml", Kind: "workflow", RepositoryID: app})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	add(Node{Key: "job:build", Kind: "job", RepositoryID: app, ContainerKey: "octo/app/ci.yml", JobID: "build"})
	add(Node{Key: "job:test", Kind: "job", RepositoryID: app, ContainerKey: "octo/app/ci.yml", JobID: "test"})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := w.AddEdge(Edge{Key: "test-needs-build", SrcKey: "job:test", DstKey: "job:build",
		Kind: EdgeKindJobNeedsJob, RepositoryID: app, FilePath: "ci.yml", StartLine: 9}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	d, err := s.NodeDetailOf("job:build", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.NamedBy != 0 {
		t.Errorf("a job another needs is named by %d uses: lines, want 0", d.NamedBy)
	}
}

// What a workflow uses is what its uses: lines name. `needs:` orders its own jobs: folded to the
// workflow, it listed swift.yml among swift.yml's own dependencies.
func TestAWorkflowsUsesLeaveOutItsOwnJobsNeeds(t *testing.T) {
	s := newTestStore(t)
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, 0)
	add := adder(t, w)
	add(Node{Key: "octo/app/ci.yml", Kind: "workflow", RepositoryID: app})
	add(Node{Key: "actions/checkout", Kind: "action", IsExternal: true})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	add(Node{Key: "job:build", Kind: "job", RepositoryID: app, ContainerKey: "octo/app/ci.yml", JobID: "build"})
	add(Node{Key: "job:test", Kind: "job", RepositoryID: app, ContainerKey: "octo/app/ci.yml", JobID: "test"})
	add(Node{Key: "step:build:0", Kind: "step", RepositoryID: app, ContainerKey: "octo/app/ci.yml", JobID: "build"})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, e := range []Edge{
		{Key: "test-needs-build", SrcKey: "job:test", DstKey: "job:build", Kind: EdgeKindJobNeedsJob,
			RepositoryID: app, FilePath: "ci.yml", StartLine: 9},
		{Key: "build-checkout", SrcKey: "step:build:0", DstKey: "actions/checkout", Kind: EdgeKindStepUsesAction,
			RefValue: "v4", RepositoryID: app, FilePath: "ci.yml", StartLine: 5},
	} {
		if err := w.AddEdge(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	d, err := s.NodeDetailOf("octo/app/ci.yml", nil)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, u := range d.Uses {
		keys = append(keys, u.Key)
	}
	if len(keys) != 1 || keys[0] != "actions/checkout" {
		t.Errorf("ci.yml uses %v, want actions/checkout alone", keys)
	}
}
