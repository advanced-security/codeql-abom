package store

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
)

// declaredLine is the one location octo/app's commits declared different things at.
const declaredLine = "octo/app:.github/workflows/ci.yml:12:9"

// forgottenLine is a location only the history commit declared.
const forgottenLine = "octo/app:.github/workflows/ci.yml:20:9"

const declaredWorkflow = "octo/app/.github/workflows/ci.yml"

// declaration is what one commit of octo/app declared at declaredLine.
type declaration struct {
	commit, target, ref string
}

// The three things one line declared: A and B at the two commits octo/app is live at, C at a
// history commit nothing resolves to any more.
var (
	declaredA = declaration{"c1", "vendor/alpha", "v1"}
	declaredB = declaration{"c2", "vendor/beta", "v2"}
	declaredC = declaration{"c3", "vendor/gamma", "v3"}
)

// declarationsFixture writes the three declarations with last one writing the row, so a view that
// read the row would show whichever commit was analyzed last. The history commit also declares
// forgottenLine, which no live commit declares.
func declarationsFixture(t *testing.T, last declaration) *Store {
	t.Helper()
	s := newTestStore(t)
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	step := "step:octo/app:.github/workflows/ci.yml:build:0"
	w := NewWriter(s, 0)
	nodes := []Node{
		{Key: declaredWorkflow, Kind: "workflow", RepositoryID: app, Path: ".github/workflows/ci.yml"},
		{Key: step, Kind: "step", RepositoryID: app, ContainerKey: declaredWorkflow},
	}
	for _, target := range []string{"vendor/alpha", "vendor/beta", "vendor/gamma", "vendor/delta"} {
		owner, repo, _ := strings.Cut(target, "/")
		nodes = append(nodes,
			Node{Key: target, Kind: "composite_action", Owner: owner, Repo: repo, IsExternal: true},
			Node{Key: target + ":action.yml:step", Kind: "step", ContainerKey: target})
	}
	for _, n := range nodes {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	order := []declaration{}
	for _, d := range []declaration{declaredA, declaredB, declaredC} {
		if d != last {
			order = append(order, d)
		}
	}
	for _, d := range append(order, last) {
		run, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
			QueryID: "abom/edges", Repository: "octo/app", DBCommit: d.commit})
		if err != nil {
			t.Fatal(err)
		}
		lines := []Edge{{Key: declaredLine, SrcKey: step, DstKey: d.target,
			Kind: EdgeKindStepUsesAction, RefRaw: d.target + "@" + d.ref, RefValue: d.ref,
			RepositoryID: app, QueryRunID: run, FilePath: ".github/workflows/ci.yml",
			StartLine: 12, StartColumn: 9}}
		if d == declaredC {
			lines = append(lines, Edge{Key: forgottenLine, SrcKey: step, DstKey: "vendor/delta",
				Kind: EdgeKindStepUsesAction, RefRaw: "vendor/delta@v4", RefValue: "v4",
				RepositoryID: app, QueryRunID: run, FilePath: ".github/workflows/ci.yml",
				StartLine: 20, StartColumn: 9})
		}
		for _, e := range lines {
			if err := w.AddEdge(e); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkSightedContent(run); err != nil {
			t.Fatal(err)
		}
	}

	// Two references resolve to octo/app, at c1 and at c2, which makes those two commits live.
	for ref, commit := range map[string]string{"main": "c1", "release": "c2"} {
		if _, err := s.DB().Exec(`
			INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
			                           completed_at, resolved_sha, analyzed_ref, analyzed_at)
			VALUES ('octo/app', ?, 'octo', 'app', 0, 'done', '2026-09-20T00:00:00Z',
			        '2026-09-21T00:00:00Z', ?, ?, '2026-09-21T00:00:00Z')`,
			ref, commit, ref); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// eachLastWriter runs a check once for each of the three commits writing the row last.
func eachLastWriter(t *testing.T, check func(t *testing.T, s *Store)) {
	for _, last := range []declaration{declaredA, declaredB, declaredC} {
		t.Run("last written at "+last.commit, func(t *testing.T) {
			check(t, declarationsFixture(t, last))
		})
	}
}

// A line whose live commits declared different things is one row per thing declared, each naming
// the commits that declared it. Showing the row instead shows whichever commit was analyzed last,
// without saying which, and a history commit's target when that one happened to be last.
func TestARelationshipIsListedOncePerThingItsLiveCommitsDeclared(t *testing.T) {
	eachLastWriter(t, func(t *testing.T, s *Store) {
		page, err := s.RelationshipsPage(RelationshipsQuery{Limit: 50})
		if err != nil {
			t.Fatalf("RelationshipsPage: %v", err)
		}
		got := map[string]string{}
		for _, r := range page.Items {
			if r.EdgeKey == declaredLine {
				got[r.Target+"@"+r.Ref] = strings.Join(r.Commits, ",")
			}
		}
		want := map[string]string{"vendor/alpha@v1": "c1", "vendor/beta@v2": "c2"}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("the line lists %v, want %v", got, want)
		}
		if page.Total != 2 || page.Lines != 1 {
			t.Errorf("Total = %d over %d lines, want 2 declarations on 1 line", page.Total, page.Lines)
		}
		// Not silently dropped from a total that used to count it.
		if page.WithoutDeclaration != 1 {
			t.Errorf("WithoutDeclaration = %d, want the line only a history commit declared",
				page.WithoutDeclaration)
		}
		// A search tests the one end such a line still has, so an unrelated one is not counted.
		elsewhere, err := s.RelationshipsPage(RelationshipsQuery{Search: "vendor/", Limit: 50})
		if err != nil {
			t.Fatalf("RelationshipsPage: %v", err)
		}
		if elsewhere.WithoutDeclaration != 0 {
			t.Errorf("searching for targets counts %d lines without a declaration whose source "+
				"does not match", elsewhere.WithoutDeclaration)
		}
		mine, err := s.RelationshipsPage(RelationshipsQuery{Search: "octo/app", Limit: 50})
		if err != nil {
			t.Fatalf("RelationshipsPage: %v", err)
		}
		if mine.WithoutDeclaration != 1 {
			t.Errorf("searching for the source counts %d lines without a declaration, want 1",
				mine.WithoutDeclaration)
		}
	})
}

// The reference style and the reference filter read what was declared, not what the row holds.
func TestNarrowingRelationshipsReadsTheDeclarationNotTheRow(t *testing.T) {
	eachLastWriter(t, func(t *testing.T, s *Store) {
		page, err := s.RelationshipsPage(RelationshipsQuery{Search: "vendor/gamma", Limit: 50})
		if err != nil {
			t.Fatalf("RelationshipsPage: %v", err)
		}
		if page.Total != 0 {
			t.Errorf("searching the history commit's target found %d rows: %+v", page.Total, page.Items)
		}
		page, err = s.RelationshipsPage(RelationshipsQuery{Search: "vendor/beta", Limit: 50})
		if err != nil {
			t.Fatalf("RelationshipsPage: %v", err)
		}
		if page.Total != 1 || page.Items[0].Ref != "v2" {
			t.Errorf("searching a live commit's target found %+v, want beta@v2", page.Items)
		}
	})
}

// `at=` keeps what something names at that version at a commit in use. A version only a history
// commit named is not what anybody runs.
func TestTheActionsUnitFindsAVersionByWhatLiveCommitsDeclare(t *testing.T) {
	eachLastWriter(t, func(t *testing.T, s *Store) {
		for at, want := range map[string][]string{
			"v1": {"vendor/alpha"}, "v2": {"vendor/beta"}, "v3": {},
		} {
			page, err := s.ActionsInventoryPage(InventoryQuery{At: []string{at}, Limit: 50})
			if err != nil {
				t.Fatalf("ActionsInventoryPage at %s: %v", at, err)
			}
			if got := inventoryKeys(page); !slices.Equal(got, want) {
				t.Errorf("at=%s lists %v, want %v", at, got, want)
			}
		}
		page, err := s.ActionsInventoryPage(InventoryQuery{RefStyle: RefStyleTag, Limit: 50})
		if err != nil {
			t.Fatalf("ActionsInventoryPage: %v", err)
		}
		if got := inventoryKeys(page); !slices.Equal(got, []string{"vendor/alpha", "vendor/beta"}) {
			t.Errorf("ref=tag lists %v, want what the live commits name by tag", got)
		}
	})
}

func inventoryKeys(page InventoryPage) []string {
	out := []string{}
	for _, a := range page.Items {
		out = append(out, a.NodeKey)
	}
	sort.Strings(out)
	return out
}

// The Explorer draws an edge for each thing a live commit declared, at the reference it declared.
func TestTheExplorerDrawsWhatLiveCommitsDeclared(t *testing.T) {
	eachLastWriter(t, func(t *testing.T, s *Store) {
		g, err := s.FullGraph(FullGraphOptions{})
		if err != nil {
			t.Fatalf("FullGraph: %v", err)
		}
		if got, want := drawnFrom(g.Edges, declaredWorkflow), "vendor/alpha@[v1] vendor/beta@[v2]"; got != want {
			t.Errorf("the workflow draws %q, want %q", got, want)
		}
		keys := map[string]bool{}
		for _, n := range g.Nodes {
			keys[n.Key] = true
		}
		for _, e := range g.Edges {
			if !keys[e.Source] || !keys[e.Target] {
				t.Errorf("edge %s -> %s has an end outside the graph", e.Source, e.Target)
			}
		}

		sub, err := s.Subgraph(SubgraphOptions{Focus: declaredWorkflow, Hops: 1,
			Direction: DirectionDependencies})
		if err != nil {
			t.Fatalf("Subgraph: %v", err)
		}
		walked := []string{}
		for _, e := range sub.Edges {
			walked = append(walked, fmt.Sprintf("%s@%v", e.Target, e.Refs))
		}
		sort.Strings(walked)
		if got, want := strings.Join(walked, " "), "vendor/alpha@[v1] vendor/beta@[v2]"; got != want {
			t.Errorf("the focus walk reaches %q, want %q", got, want)
		}
	})
}

func drawnFrom(edges []GraphEdge, source string) string {
	out := []string{}
	for _, e := range edges {
		if e.Source == source {
			out = append(out, fmt.Sprintf("%s@%v", e.Target, e.Refs))
		}
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// A dependency drawn once per version hangs each edge out of it from the version whose commit
// declared it. Hanging both from the newest analysis would say v1 runs what only v2 declares.
func TestAnEdgeOutOfADependencyHangsFromTheVersionWhoseCommitDeclaredIt(t *testing.T) {
	for _, last := range []string{"c1", "c2"} {
		t.Run("last written at "+last, func(t *testing.T) {
			s := newTestStore(t)
			declared := map[string]string{"c1": "v3", "c2": "v4"}
			for _, commit := range []string{"c1", "c2"} {
				if commit != last {
					observeAt(t, s, commit, sharedLocation, "actions/cache", declared[commit], "")
				}
			}
			observeAt(t, s, last, sharedLocation, "actions/cache", declared[last], "")
			for ref, commit := range map[string]string{"v1": "c1", "v2": "c2"} {
				if _, err := s.DB().Exec(`
					INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
					                           completed_at, resolved_sha, analyzed_ref, analyzed_at)
					VALUES ('vendor/comp', ?, 'vendor', 'comp', 1, 'done', '2026-09-20T00:00:00Z',
					        '2026-09-21T00:00:00Z', ?, ?, ?)`,
					ref, commit, ref, "2026-09-2"+commit[1:]+"T00:00:00Z"); err != nil {
					t.Fatal(err)
				}
			}

			g, err := s.FullGraph(FullGraphOptions{})
			if err != nil {
				t.Fatalf("FullGraph: %v", err)
			}
			for source, want := range map[string]string{
				"vendor/comp@v1": "actions/cache@[v3]", "vendor/comp@v2": "actions/cache@[v4]",
			} {
				if got := drawnFrom(g.Edges, source); got != want {
					t.Errorf("%s draws %q, want %q", source, got, want)
				}
			}
		})
	}
}

// Pinning counts the references a live commit declares, each as it was written there.
func TestPinningCountsWhatLiveCommitsDeclared(t *testing.T) {
	eachLastWriter(t, func(t *testing.T, s *Store) {
		leaves, err := s.PinningLevel(PinningByReference, declaredWorkflow, PinningFilter{}, 0)
		if err != nil {
			t.Fatalf("PinningLevel: %v", err)
		}
		got := []string{}
		for _, l := range leaves {
			got = append(got, l.ScopeKey+"@"+l.Ref)
		}
		sort.Strings(got)
		if want := []string{"vendor/alpha@v1", "vendor/beta@v2"}; !slices.Equal(got, want) {
			t.Errorf("the workflow's references are %v, want %v", got, want)
		}
		total, err := s.PinningTotals(PinningFilter{})
		if err != nil {
			t.Fatalf("PinningTotals: %v", err)
		}
		if total.Pinnable != 2 {
			t.Errorf("Pinnable = %d, want the two references live commits declare", total.Pinnable)
		}

		pins, err := s.UnanalyzedCompositePins()
		if err != nil {
			t.Fatalf("UnanalyzedCompositePins: %v", err)
		}
		queued := []string{}
		for _, p := range pins {
			queued = append(queued, p.NodeKey+"@"+p.Ref)
		}
		sort.Strings(queued)
		if want := []string{"vendor/alpha@v1", "vendor/beta@v2"}; !slices.Equal(queued, want) {
			t.Errorf("the unanalyzed pins are %v, want %v", queued, want)
		}
	})
}

// A bill of materials lists what the roots' live commits declare, and nothing only history named.
func TestTheBillOfMaterialsListsWhatLiveCommitsDeclared(t *testing.T) {
	eachLastWriter(t, func(t *testing.T, s *Store) {
		doc, err := s.BillOfMaterials(BOMOptions{Roots: []string{"octo/app"}})
		if err != nil {
			t.Fatalf("BillOfMaterials: %v", err)
		}
		got := []string{}
		for _, c := range doc.Components {
			for _, r := range c.References {
				got = append(got, c.Key+"@"+r.Value)
			}
		}
		sort.Strings(got)
		if want := []string{"vendor/alpha@v1", "vendor/beta@v2"}; !slices.Equal(got, want) {
			t.Errorf("the document names %v, want %v", got, want)
		}
	})
}

func (s *Store) liveAnswerLoads() int {
	c := s.declarationCache()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loads
}

// A newly ingested edge is read by the next request, and LiveCommits is read once per database
// state rather than once per request.
func TestDeclarationsAreReadOncePerDatabaseStateAndNeverLag(t *testing.T) {
	s := declarationsFixture(t, declaredC)
	state, err := s.connectionState()
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := s.RelationshipsPage(RelationshipsQuery{Limit: 50}); err != nil {
			t.Fatalf("RelationshipsPage: %v", err)
		}
	}
	if got := s.liveAnswerLoads(); got != 1 {
		t.Errorf("three requests over one database state read LiveCommits %d times, want 1", got)
	}
	// Reading must not move the state the walk's graph is keyed on, or each would reload the other.
	if after, err := s.connectionState(); err != nil || after != state {
		t.Errorf("reading the declarations moved the database state from %+v to %+v (%v)",
			state, after, err)
	}

	run, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "abom/edges", Repository: "octo/app", DBCommit: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	app := repoID(t, s, "octo/app")
	w := NewWriter(s, 0)
	step := "step:octo/app:.github/workflows/ci.yml:build:0"
	for _, n := range []Node{
		{Key: declaredWorkflow, Kind: "workflow", RepositoryID: app, Path: ".github/workflows/ci.yml"},
		{Key: step, Kind: "step", RepositoryID: app, ContainerKey: declaredWorkflow},
		{Key: "vendor/delta", Kind: "composite_action", Owner: "vendor", Repo: "delta", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.AddEdge(Edge{Key: "octo/app:.github/workflows/ci.yml:30:9",
		SrcKey: step, DstKey: "vendor/delta",
		Kind: EdgeKindStepUsesAction, RefRaw: "vendor/delta@v9", RefValue: "v9",
		RepositoryID: app, QueryRunID: run, FilePath: ".github/workflows/ci.yml",
		StartLine: 30, StartColumn: 9}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	page, err := s.RelationshipsPage(RelationshipsQuery{Search: "vendor/delta", Limit: 50})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	if page.Total != 1 || page.Items[0].Ref != "v9" {
		t.Errorf("the edge just ingested lists as %+v, want vendor/delta@v9", page.Items)
	}
	if got := s.liveAnswerLoads(); got != 2 {
		t.Errorf("LiveCommits was read %d times across two database states, want 2", got)
	}
}
