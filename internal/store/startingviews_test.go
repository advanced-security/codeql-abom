package store

import (
	"slices"
	"testing"
)

// resolvedAt queues a row whose reference resolved to a commit: depth 0 for a repository's own
// branch, deeper for a reference somebody made to it.
func resolvedAt(t *testing.T, s *Store, nodeKey, ref string, depth int, commit string) {
	t.Helper()
	owner, repo, _ := splitRepository(nodeKey)
	if _, err := s.db.Exec(`INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status,
	                                                   enqueued_at, resolved_sha)
		VALUES (?, ?, ?, ?, ?, 'done', datetime('now'), ?)`,
		nodeKey, ref, owner, repo, depth, commit); err != nil {
		t.Fatalf("queueing %s@%s: %v", nodeKey, ref, err)
	}
}

// startingViewsFixture is a repository under review that is also somebody's dependency:
//
//	acme/app   ci.yml at a1, its default branch -> acme/tool@v1, which resolves to r1
//	acme/tool  ci.yml at d1, its default branch -> actions/checkout@v4
//	acme/tool  ci.yml at r1, the release v1     -> actions/checkout@v3, actions/cache@v2
//
// r1 is live because acme/app runs acme/tool@v1, which is the action and not acme/tool's CI.
func startingViewsFixture(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/app")
	seedOwnedRepo(t, s, "acme/tool")
	declaredAt(t, s, "acme/app", "a1", ".github/workflows/ci.yml", 9, "acme/tool", "v1")
	declaredAt(t, s, "acme/tool", "d1", ".github/workflows/ci.yml", 10, "actions/checkout", "v4")
	declaredAt(t, s, "acme/tool", "r1", ".github/workflows/ci.yml", 12, "actions/checkout", "v3")
	declaredAt(t, s, "acme/tool", "r1", ".github/workflows/ci.yml", 14, "actions/cache", "v2")
	resolvedAt(t, s, "acme/app", "", 0, "a1")
	resolvedAt(t, s, "acme/tool", "v1", 1, "r1")
	resolvedAt(t, s, "acme/tool", "", 0, "d1")
	return s
}

// A starting point's own CI runs at its default branch. At a release only a consumer pins it runs
// for nobody, so the lines only that release's CI declared are not relationships anybody has.
func TestRelationshipsListAStartingPointsCIOnlyWhereItRuns(t *testing.T) {
	s := startingViewsFixture(t)

	page, err := s.RelationshipsPage(RelationshipsQuery{Limit: 100})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	var tool []string
	for _, r := range page.Items {
		if r.Repository == "acme/tool" {
			tool = append(tool, r.Target+"@"+r.Ref)
		}
	}
	if !slices.Equal(tool, []string{"actions/checkout@v4"}) {
		t.Errorf("acme/tool's relationships are %v, want only what its default branch declares", tool)
	}
	if page.Total != 2 || page.Lines != 2 {
		t.Errorf("relationships total %d on %d lines, want acme/app's and acme/tool's default "+
			"branch's: 2 on 2", page.Total, page.Lines)
	}
}

// Pinning measures the lines a repository under review runs, so a release's CI is not one of them.
func TestPinningCountsAStartingPointsCIOnlyWhereItRuns(t *testing.T) {
	s := startingViewsFixture(t)

	total, err := s.PinningTotals(PinningFilter{})
	if err != nil {
		t.Fatalf("PinningTotals: %v", err)
	}
	if total.Pinnable != 2 {
		t.Errorf("pinnable references are %d, want acme/app's and acme/tool's default branch's 2",
			total.Pinnable)
	}
}

// The bill of materials is what the roots run, so nothing only a release's CI names is in it.
func TestTheBillOfMaterialsListsAStartingPointsCIOnlyWhereItRuns(t *testing.T) {
	s := startingViewsFixture(t)

	bom, err := s.BillOfMaterials(BOMOptions{})
	if err != nil {
		t.Fatalf("BillOfMaterials: %v", err)
	}
	refs := map[string][]string{}
	for _, c := range bom.Components {
		for _, r := range c.References {
			refs[c.Key] = append(refs[c.Key], r.Value)
		}
	}
	if _, ok := refs["actions/cache"]; ok {
		t.Errorf("the bill of materials lists actions/cache, which only acme/tool's release CI names")
	}
	if got := refs["actions/checkout"]; !slices.Equal(got, []string{"v4"}) {
		t.Errorf("actions/checkout is listed at %v, want v4 from acme/tool's default branch", got)
	}
}

// The Explorer draws what runs, so a release's CI draws no edge.
func TestTheExplorerDrawsAStartingPointsCIOnlyWhereItRuns(t *testing.T) {
	s := startingViewsFixture(t)

	g, err := s.FullGraph(FullGraphOptions{IncludeSteps: true})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	var drawn []string
	for _, e := range g.Edges {
		if e.Target == "actions/cache" || e.Source == "acme/tool/.github/workflows/ci.yml:step:12" ||
			e.Source == "acme/tool/.github/workflows/ci.yml:step:14" {
			drawn = append(drawn, e.Source+" -> "+e.Target)
		}
	}
	if len(drawn) > 0 {
		t.Errorf("the Explorer draws %v, which only acme/tool's release CI declares", drawn)
	}
	if !slices.ContainsFunc(g.Edges, func(e GraphEdge) bool {
		return e.Source == "acme/tool/.github/workflows/ci.yml:step:10" && e.Target == "actions/checkout"
	}) {
		t.Error("the Explorer does not draw acme/tool's default branch's actions/checkout")
	}
}

// usersOfVersions reads who names each version, from the graph's reference states and the queue,
// and fails where the two disagree: they answer one question.
func usersOfVersions(t *testing.T, s *Store) map[string]int {
	t.Helper()
	states, err := s.refStates()
	if err != nil {
		t.Fatalf("refStates: %v", err)
	}
	items, err := s.FrontierSnapshot("", 500)
	if err != nil {
		t.Fatalf("FrontierSnapshot: %v", err)
	}
	out := map[string]int{}
	for _, it := range items {
		st := states[[2]string{it.NodeKey, it.Ref}]
		if st.dependent != it.UsedByCount {
			t.Errorf("%s@%s: the graph says %d repositories name it and the queue says %d",
				it.NodeKey, it.Ref, st.dependent, it.UsedByCount)
		}
		out[it.NodeKey+"@"+it.Ref] = it.UsedByCount
	}
	return out
}

// A version's users are those whose lines name it where those lines run: not what the last
// analysis wrote into a row, and not a starting point's CI at a release a consumer pins.
func TestAVersionsUsersAreReadWhereTheirLinesRun(t *testing.T) {
	t.Run("the row's last writer is a commit nothing resolves to", func(t *testing.T) {
		s := protectedFixture(t)
		declaredAt(t, s, "acme/service", "x9", ".github/workflows/ci.yml", 10, "actions/cache", "v3")
		enqueueRef(t, s, "actions/checkout", "v4", 1, "pending")
		enqueueRef(t, s, "actions/cache", "v3", 1, "pending")

		got := usersOfVersions(t, s)
		if got["actions/checkout@v4"] != 1 || got["actions/cache@v3"] != 0 {
			t.Errorf("users are %v; want actions/checkout@v4 named once, at m1, and "+
				"actions/cache@v3 by nobody", got)
		}
	})
	t.Run("a starting point is a dependency at a release", func(t *testing.T) {
		s := startingViewsFixture(t)
		for _, ref := range []string{"v3", "v4"} {
			enqueueRef(t, s, "actions/checkout", ref, 2, "pending")
		}
		enqueueRef(t, s, "actions/cache", "v2", 2, "pending")

		got := usersOfVersions(t, s)
		want := map[string]int{"acme/app@": 0, "acme/tool@": 0, "acme/tool@v1": 1,
			"actions/checkout@v4": 1, "actions/checkout@v3": 0, "actions/cache@v2": 0}
		for version, n := range want {
			if got[version] != n {
				t.Errorf("%s is named by %d repositories, want %d; all: %v", version, got[version], n, got)
			}
		}
	})
}

// A workflow file is somebody's dependency once another repository calls it, and then it runs
// wherever that caller's reference resolves, release or not. Only CI nobody else calls is a
// starting point's alone.
func calledWorkflowFixture(t *testing.T) *Store {
	t.Helper()
	s := startingViewsFixture(t)
	declaredAt(t, s, "acme/tool", "r1", ".github/workflows/shared.yml", 3,
		"actions/upload-artifact", "v4")
	app, err := s.UpsertRepository("acme/app", "")
	if err != nil {
		t.Fatal(err)
	}
	tool, err := s.UpsertRepository("acme/tool", "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular, QueryID: "abom/edges",
		Repository: "acme/app", DBCommit: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "acme/tool/.github/workflows/shared.yml", Kind: "workflow", RepositoryID: tool,
			Path: ".github/workflows/shared.yml"},
		{Key: "acme/app/.github/workflows/ci.yml:job:release", Kind: "job", RepositoryID: app,
			ContainerKey: "acme/app/.github/workflows/ci.yml"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.AddEdge(Edge{Key: "acme/app:.github/workflows/ci.yml:20:5",
		SrcKey: "acme/app/.github/workflows/ci.yml:job:release",
		DstKey: "acme/tool/.github/workflows/shared.yml", Kind: EdgeKindJobUsesReusableWorkflow,
		RefRaw: "acme/tool/.github/workflows/shared.yml@v1", RefValue: "v1", RepositoryID: app,
		QueryRunID: run, FilePath: ".github/workflows/ci.yml", StartLine: 20, StartColumn: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAWorkflowAnotherRepositoryCallsRunsWhereItsCallerResolvesIt(t *testing.T) {
	for _, origin := range []string{OriginSeed, OriginUpstream} {
		t.Run(origin, func(t *testing.T) {
			s := calledWorkflowFixture(t)
			if origin == OriginUpstream {
				if _, err := s.db.Exec(`UPDATE repository SET origin = 'upstream'
					WHERE repository_full_name = 'acme/tool';
					DELETE FROM abom_frontier WHERE node_key = 'acme/tool' AND depth = 0`); err != nil {
					t.Fatal(err)
				}
			}
			page, err := s.RelationshipsPage(RelationshipsQuery{Limit: 100})
			if err != nil {
				t.Fatalf("RelationshipsPage: %v", err)
			}
			if !slices.ContainsFunc(page.Items, func(r RelationshipSummary) bool {
				return r.Repository == "acme/tool" && r.Target == "actions/upload-artifact"
			}) {
				t.Errorf("the called shared.yml lists nothing at r1: %+v", page.Items)
			}
			doc := build(t, s, BOMOptions{Roots: []string{"acme/app"}})
			if componentOf(doc, "actions/upload-artifact") == nil {
				t.Error("the called workflow's action is missing from the BOM")
			}
			g, err := s.FullGraph(FullGraphOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(g.Nodes, func(n GraphNode) bool {
				return n.Key == "acme/tool/.github/workflows/shared.yml"
			}) {
				t.Error("the called workflow is missing from the Explorer")
			}
		})
	}
}
