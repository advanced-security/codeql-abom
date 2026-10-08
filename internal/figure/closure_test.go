package figure

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// graphOf builds a projection from "a>b" pairs, adding an action for any key not given as a node.
func graphOf(nodes []store.GraphNode, uses ...string) store.Graph {
	g := store.Graph{Nodes: nodes}
	have := map[string]bool{}
	for _, n := range nodes {
		have[n.Key] = true
	}
	for _, pair := range uses {
		// The last ">", so a hostile source key may contain one.
		at := strings.LastIndex(pair, ">")
		if at < 0 {
			panic("not a pair: " + pair)
		}
		src, dst := pair[:at], pair[at+1:]
		for _, k := range []string{src, dst} {
			if !have[k] {
				g.Nodes = append(g.Nodes, store.GraphNode{Key: k, Kind: "action"})
				have[k] = true
			}
		}
		g.Edges = append(g.Edges, store.GraphEdge{Source: src, Target: dst,
			Kind: store.EdgeKindStepUsesAction, Refs: []string{"v1"}, Count: 1})
	}
	return g
}

func drawn(c Closure) []string {
	out := []string{}
	for _, n := range c.Nodes {
		out = append(out, n.Key)
	}
	return out
}

// By the operator's decision, what you own comes first, with the path that connects it: an owned
// workflow two steps away is drawn before an external caller one step away.
func TestAnOwnedNodeTwoAwayIsDrawnBeforeAnExternalOneAStepAwayWithItsPath(t *testing.T) {
	g := graphOf([]store.GraphNode{
		{Key: "o/app/ci.yml", Kind: "workflow", Owned: true},
	}, "x/near>r/act", "o/app/ci.yml>x/via", "x/via>r/act")

	c := Cut(g, "r/act", CutOptions{Limit: 3})
	if got, want := drawn(c), []string{"o/app/ci.yml", "r/act", "x/via"}; !slices.Equal(got, want) {
		t.Fatalf("drew %v, want %v: yours and its path before the nearer caller", got, want)
	}
	if c.Counts.InClosure != 4 || c.Counts.LeftOut != 1 {
		t.Errorf("counts %+v, want 4 in the closure and 1 left out", c.Counts)
	}
}

// Walked undirected, a closure climbs into everything else that runs what this runs. The figure
// is the two directed walks, and nothing a sibling holds.
func TestTheClosureIsDirectedBothWaysAndNeverUndirected(t *testing.T) {
	uses := []string{"o/app/ci.yml>r/act", "r/act>dep/lib"}
	for i := range 50 {
		uses = append(uses, fmt.Sprintf("sib/w%02d.yml>dep/lib", i))
	}
	c := Cut(graphOf(nil, uses...), "r/act", CutOptions{})
	if got, want := drawn(c), []string{"dep/lib", "o/app/ci.yml", "r/act"}; !slices.Equal(got, want) {
		t.Fatalf("drew %v, want only what runs r/act and what it runs", got)
	}
	if c.Counts.InClosure != 3 {
		t.Errorf("the closure holds %d nodes, want 3", c.Counts.InClosure)
	}
}

// A figure never holds more than the limit, and says how much it left out. The closure is built
// past the cap, so raising the cap cannot make this pass by drawing everything.
func TestNoFigureHoldsMoreThanTheLimitAndSaysWhatItLeftOut(t *testing.T) {
	const extra = 20
	var uses []string
	for i := range DefaultLimit + extra - 1 {
		uses = append(uses, fmt.Sprintf("c/w%04d.yml>r/act", i))
	}
	c := Cut(graphOf(nil, uses...), "r/act", CutOptions{})
	if len(c.Nodes) != DefaultLimit || c.Counts.Drawn != DefaultLimit {
		t.Fatalf("drew %d nodes, want the limit of %d", len(c.Nodes), DefaultLimit)
	}
	if c.Counts.InClosure != DefaultLimit+extra || c.Counts.LeftOut != extra {
		t.Errorf("counts %+v, want %d in the closure and %d left out", c.Counts, DefaultLimit+extra, extra)
	}
}

// A drawn node with no path to the page's node would assert a relationship the figure does not
// show. Every node reaches the page's node, or is reached from it, through drawn edges alone.
func TestEveryDrawnNodeIsConnectedToThePagesNodeWithinTheDrawing(t *testing.T) {
	var nodes []store.GraphNode
	var uses []string
	for i := range 30 {
		mid := fmt.Sprintf("m/mid%02d", i)
		uses = append(uses, mid+">r/act", fmt.Sprintf("c/w%02d.yml>%s", i, mid))
		nodes = append(nodes, store.GraphNode{Key: fmt.Sprintf("c/w%02d.yml", i), Kind: "workflow",
			Owned: i%3 == 0})
		uses = append(uses, fmt.Sprintf("r/act>d/dep%02d", i), fmt.Sprintf("d/dep%02d>d/deep%02d", i, i))
	}
	c := Cut(graphOf(nodes, uses...), "r/act", CutOptions{})
	if len(c.Nodes) > DefaultLimit {
		t.Fatalf("drew %d nodes", len(c.Nodes))
	}
	toRoot, fromRoot := map[string][]string{}, map[string][]string{}
	for _, e := range c.Edges {
		toRoot[e.Source] = append(toRoot[e.Source], e.Target)
		fromRoot[e.Target] = append(fromRoot[e.Target], e.Source)
	}
	reaches := func(from string, next map[string][]string) bool {
		seen := map[string]bool{}
		queue := []string{from}
		for len(queue) > 0 {
			k := queue[0]
			queue = queue[1:]
			if k == "r/act" {
				return true
			}
			for _, n := range next[k] {
				if !seen[n] {
					seen[n] = true
					queue = append(queue, n)
				}
			}
		}
		return false
	}
	for _, n := range c.Nodes {
		switch n.Side {
		case SideCaller:
			if !reaches(n.Key, toRoot) {
				t.Errorf("caller %s has no drawn path to the page's node", n.Key)
			}
		case SideDependency:
			if !reaches(n.Key, fromRoot) {
				t.Errorf("dependency %s has no drawn path from the page's node", n.Key)
			}
		}
	}
	owned := 0
	for _, n := range c.Nodes {
		if n.Owned {
			owned++
		}
	}
	if owned != 10 {
		t.Errorf("drew %d of the 10 owned workflows; each fits with its path, so all of them go first", owned)
	}
}

// What runs it is drawn as What uses: this lists it: the page's terms choose where routes start,
// and the path from each is drawn. A caller the terms leave out is counted, not drawn.
func TestCallersAreTheRoutesThePagesTermsList(t *testing.T) {
	g := graphOf([]store.GraphNode{
		{Key: "o/app/ci.yml", Kind: "workflow"},
		{Key: "u/up/ci.yml", Kind: "workflow"},
		{Key: "x/shared@v1", Kind: "reusable_workflow", Ref: "v1"},
	}, "o/app/ci.yml>x/shared@v1", "x/shared@v1>r/act", "u/up/ci.yml>r/act", "r/act>d/dep")

	c := Cut(g, "r/act", CutOptions{Callers: map[string]bool{"o/app/ci.yml": true}})
	if got, want := drawn(c), []string{"d/dep", "o/app/ci.yml", "r/act", "x/shared@v1"}; !slices.Equal(got, want) {
		t.Fatalf("drew %v, want %v", got, want)
	}
	if c.Counts.NotListed != 1 {
		t.Errorf("%d callers counted as not listed, want u/up's", c.Counts.NotListed)
	}
}

// The page's own node is drawn once per version in use, widest reach first, and `at` narrows them.
func TestVersionsAreDrawnWidestFirstAndAtNarrowsThem(t *testing.T) {
	versions := []store.GraphNode{
		{Key: "r/act@v1", Kind: "action", Ref: "v1", DependentRepos: 5},
		{Key: "r/act@v2", Kind: "action", Ref: "v2", DependentRepos: 9},
		{Key: "r/act@v3", Kind: "action", Ref: "v3", DependentRepos: 1},
	}
	g := graphOf(versions, "c/a.yml>r/act@v1", "c/b.yml>r/act@v2", "c/c.yml>r/act@v3")

	c := Cut(g, "r/act", CutOptions{MaxVersions: 2})
	roots := []string{}
	for _, n := range c.Nodes {
		if n.Side == SideRoot {
			roots = append(roots, n.Key)
		}
	}
	if !slices.Equal(roots, []string{"r/act@v1", "r/act@v2"}) || c.Counts.Versions != 3 ||
		c.Counts.VersionsDrawn != 2 {
		t.Errorf("drew versions %v with counts %+v, want the widest two of three", roots, c.Counts)
	}
	if slices.Contains(drawn(c), "c/c.yml") {
		t.Error("drew what runs a version the figure left out")
	}

	at := Cut(g, "r/act", CutOptions{At: []string{"v3"}})
	if got := drawn(at); !slices.Equal(got, []string{"c/c.yml", "r/act@v3"}) {
		t.Errorf("at=v3 drew %v", got)
	}
}

// An edge leading to a finding or an advisory is emphasized, and only that one.
func TestOnlyRoutesToAFindingOrAnAdvisoryAreMarked(t *testing.T) {
	g := graphOf([]store.GraphNode{
		{Key: "d/bad", Kind: "action", Findings: 2},
		{Key: "d/clean", Kind: "action"},
	}, "c/ci.yml>r/act", "r/act>d/bad", "r/act>d/clean")
	c := Cut(g, "r/act", CutOptions{})
	marked := map[string]bool{}
	for _, e := range c.Edges {
		marked[e.Source+">"+e.Target] = e.Route
	}
	want := map[string]bool{"c/ci.yml>r/act": true, "r/act>d/bad": true, "r/act>d/clean": false}
	for edge, route := range want {
		if marked[edge] != route {
			t.Errorf("%s marked %v, want %v", edge, marked[edge], route)
		}
	}
}

// A caller that also uses a dependency of the page's node lies on no directed path through it,
// so that edge is not drawn.
func TestAnEdgeOnNoPathThroughThePagesNodeIsNotDrawn(t *testing.T) {
	c := Cut(graphOf(nil, "c/ci.yml>r/act", "r/act>d/dep", "c/ci.yml>d/dep"), "r/act", CutOptions{})
	for _, e := range c.Edges {
		if e.Source == "c/ci.yml" && e.Target == "d/dep" {
			t.Error("drew an edge that runs around the page's node")
		}
	}
	if len(c.Edges) != 2 {
		t.Errorf("drew %d edges, want 2", len(c.Edges))
	}
}

// Containment is a drawing aid and never a route.
func TestContainmentIsNeverWalked(t *testing.T) {
	g := graphOf(nil, "c/ci.yml>r/act")
	g.Nodes = append(g.Nodes, store.GraphNode{Key: "repo:r/act", Kind: "repo"})
	g.Edges = append(g.Edges, store.GraphEdge{Source: "repo:r/act", Target: "r/act",
		Kind: store.EdgeKindRepoContains})
	if got := drawn(Cut(g, "r/act", CutOptions{})); !slices.Equal(got, []string{"c/ci.yml", "r/act"}) {
		t.Errorf("drew %v, want what runs it and nothing that holds it", got)
	}
}

// Among what is equally yours and equally near, each version gets its first caller before any
// version gets a second: twelve versions of checkout and thirteen callers drew five versions with
// callers and seven alone.
func TestEveryVersionGetsACallerBeforeAnyGetsASecond(t *testing.T) {
	g := graphOf([]store.GraphNode{
		{Key: "r/act@v1", Kind: "action", Ref: "v1", DependentRepos: 9},
		{Key: "r/act@v2", Kind: "action", Ref: "v2", DependentRepos: 5},
	}, "c/a1.yml>r/act@v1", "c/a2.yml>r/act@v1", "c/a3.yml>r/act@v1", "c/b1.yml>r/act@v2")
	c := Cut(g, "r/act", CutOptions{Limit: 4})
	if got, want := drawn(c), []string{"c/a1.yml", "c/b1.yml", "r/act@v1", "r/act@v2"}; !slices.Equal(got, want) {
		t.Errorf("drew %v, want each version's first caller", got)
	}
}

// A version only what the page's terms leave out runs is not in use under those terms, so it takes
// no place in the figure: twelve versions of checkout drew four that nothing listed runs.
func TestAVersionNothingListedRunsIsNotDrawn(t *testing.T) {
	g := graphOf([]store.GraphNode{
		{Key: "r/act@v1", Kind: "action", Ref: "v1", DependentRepos: 1},
		{Key: "r/act@v2", Kind: "action", Ref: "v2", DependentRepos: 9},
	}, "o/a.yml>r/act@v1", "u/up.yml>r/act@v2", "r/act@v2>d/only-v2")
	c := Cut(g, "r/act", CutOptions{Callers: map[string]bool{"o/a.yml": true}})
	if got, want := drawn(c), []string{"o/a.yml", "r/act@v1"}; !slices.Equal(got, want) {
		t.Errorf("drew %v, want the version something listed runs", got)
	}
	if c.Counts.Versions != 1 || c.Counts.NotListed != 1 {
		t.Errorf("counts %+v, want one version in use and one caller not listed", c.Counts)
	}
}

// Every route from a listed start is drawn, not only the shortest: a workflow running one version
// directly and another through a reusable workflow runs both.
func TestEveryRouteFromAListedStartIsDrawn(t *testing.T) {
	g := graphOf([]store.GraphNode{
		{Key: "r/act@v1", Kind: "action", Ref: "v1"},
		{Key: "r/act@v2", Kind: "action", Ref: "v2"},
	}, "o/a.yml>r/act@v1", "o/a.yml>x/reuse.yml", "x/reuse.yml>r/act@v2")
	c := Cut(g, "r/act", CutOptions{Callers: map[string]bool{"o/a.yml": true}})
	if got, want := drawn(c), []string{"o/a.yml", "r/act@v1", "r/act@v2", "x/reuse.yml"}; !slices.Equal(got, want) {
		t.Errorf("drew %v, want both routes", got)
	}
}
