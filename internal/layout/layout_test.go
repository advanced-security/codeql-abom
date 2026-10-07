package layout

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/graphexport"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func fixtureDOT(t *testing.T, groupBy string) []byte {
	t.Helper()
	g := store.Graph{
		Nodes: []store.GraphNode{
			{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow", Repository: "octo/app",
				ReachKnown: true},
			{Key: "octo/widget", Kind: "action", Owner: "octo", Repository: "octo/widget",
				DependentRepos: 2, ReachKnown: true},
			{Key: "acme/build", Kind: "action", Owner: "acme", IsExternal: true},
		},
		Edges: []store.GraphEdge{
			{Source: "octo/app/.github/workflows/ci.yml", Target: "octo/widget",
				Kind: "step-uses-action", Refs: []string{"v4"}, Count: 1},
			{Source: "octo/app/.github/workflows/ci.yml", Target: "acme/build",
				Kind: "step-uses-action", Refs: []string{"v1"}, Count: 1},
		},
	}
	dot, err := graphexport.DOT(g, graphexport.Options{GroupBy: groupBy})
	if err != nil {
		t.Fatalf("encoding the fixture: %v", err)
	}
	return dot
}

// requireGraphviz fails rather than skips: Graphviz is a requirement, and a test that skipped
// without it would pass on exactly the machine that cannot serve.
func requireGraphviz(t *testing.T, e Engine) {
	t.Helper()
	if !Available(e) {
		t.Fatalf("%s is not installed: %s", e, Remedy)
	}
}

// Server layouts must be repeatable so the interface can depend on their coordinates.
func TestTheSameGraphLaysOutIdentically(t *testing.T) {
	requireGraphviz(t, Dot)
	dot := fixtureDOT(t, "")

	first, err := Compute(context.Background(), dot, Options{Engine: Dot})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	second, err := Compute(context.Background(), dot, Options{Engine: Dot})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(first.Positions) != 3 {
		t.Fatalf("%d positions, want one per node", len(first.Positions))
	}
	for i := range first.Positions {
		if first.Positions[i] != second.Positions[i] {
			t.Errorf("node %s moved between runs: %v then %v",
				first.Positions[i].Key, first.Positions[i], second.Positions[i])
		}
	}
}

// Force engines need an explicit seed to be repeatable.
func TestAForceEngineIsSeededSoItTooIsRepeatable(t *testing.T) {
	requireGraphviz(t, Fdp)
	dot := fixtureDOT(t, "")

	first, err := Compute(context.Background(), dot, Options{Engine: Fdp})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	second, err := Compute(context.Background(), dot, Options{Engine: Fdp})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	for i := range first.Positions {
		if first.Positions[i] != second.Positions[i] {
			t.Errorf("%s moved between runs of an unseeded force engine", first.Positions[i].Key)
		}
	}
}

// Unknown engine names must not become process names.
func TestAnUnknownEngineIsRefusedBeforeAnythingRuns(t *testing.T) {
	for _, engine := range []Engine{"rm", "dot; rm -rf /", "", "../../bin/sh"} {
		_, err := Compute(context.Background(), fixtureDOT(t, ""), Options{Engine: engine})
		if err == nil {
			t.Errorf("engine %q was accepted", engine)
			continue
		}
		if !strings.Contains(err.Error(), "not a layout engine") {
			t.Errorf("engine %q refused with the wrong reason: %v", engine, err)
		}
	}
}

// Cluster boxes are the group boundaries the caller draws.
func TestClusterBoundariesComeBackWithTheirBox(t *testing.T) {
	requireGraphviz(t, Dot)

	result, err := Compute(context.Background(), fixtureDOT(t, "owner"), Options{Engine: Dot})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(result.Clusters) != 2 {
		t.Fatalf("%d clusters, want one per owner: %+v", len(result.Clusters), result.Clusters)
	}
	for _, c := range result.Clusters {
		if c.X2 <= c.X1 || c.Y2 <= c.Y1 {
			t.Errorf("cluster %s has no area: %+v", c.Name, c)
		}
		if c.Name == "" {
			t.Error("a cluster came back with no name, so nothing can label it")
		}
	}
}

// Layout work must stop when its request is canceled.
func TestLayoutStopsWhenTheCallerGivesUp(t *testing.T) {
	requireGraphviz(t, Dot)

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	if _, err := Compute(ctx, fixtureDOT(t, ""), Options{Engine: Dot}); err == nil {
		t.Error("a canceled layout still returned a result")
	}
}

// Missing optional Graphviz is reported distinctly so callers can degrade.
func TestAMissingEngineIsReportedRatherThanHidden(t *testing.T) {
	_, err := Compute(context.Background(), fixtureDOT(t, ""), Options{
		Engine: Dot,
		// A name nothing will resolve, standing in for a machine with no Graphviz.
		binary: "abom-no-such-layout-engine",
	})
	if err == nil {
		t.Fatal("a missing engine was not reported")
	}
	if !ErrUnavailable(err) {
		t.Errorf("a missing engine is not distinguishable from a failed one: %v", err)
	}
}

// Every node must get a server position; otherwise the browser mixes layout modes.
func TestEveryNodeComesBackWithAPosition(t *testing.T) {
	requireGraphviz(t, Dot)

	result, err := Compute(context.Background(), fixtureDOT(t, "owner"), Options{Engine: Dot})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	want := map[string]bool{
		"octo/app/.github/workflows/ci.yml": true,
		"octo/widget":                       true,
		"acme/build":                        true,
	}
	for _, p := range result.Positions {
		delete(want, p.Key)
	}
	if len(want) > 0 {
		t.Errorf("no position for %v", want)
	}
}

// Cluster support is a Graphviz behavior this package exposes to callers.
func TestOnlySomeEnginesDrawAClusterBoundary(t *testing.T) {
	dot := fixtureDOT(t, "owner")
	for _, e := range Engines() {
		requireGraphviz(t, e)
		result, err := Compute(context.Background(), dot, Options{Engine: e})
		if err != nil {
			t.Fatalf("%s: %v", e, err)
		}
		drew := len(result.Clusters) > 0
		if drew != SupportsClusters(e) {
			t.Errorf("%s drew %d boundaries, but SupportsClusters says %t",
				e, len(result.Clusters), SupportsClusters(e))
		}
	}
}

// fannedDOT exposes whether broad sibling ranks keep group boundaries readable.
func fannedDOT(t *testing.T) []byte {
	t.Helper()
	const actions = 12
	g := store.Graph{}
	for a := range actions {
		g.Nodes = append(g.Nodes, store.GraphNode{
			Key: fmt.Sprintf("vendor/action%02d", a), Kind: "action", Owner: "vendor",
			IsExternal: true,
		})
	}
	for r := range 7 {
		repo := fmt.Sprintf("octo/repo%d", r)
		g.Nodes = append(g.Nodes, store.GraphNode{
			Key: "repo:" + repo, Kind: "repo", Repository: repo, ReachKnown: true,
		})
		for w := range 2 {
			wf := fmt.Sprintf("%s/.github/workflows/w%d.yml", repo, w)
			g.Nodes = append(g.Nodes, store.GraphNode{
				Key: wf, Kind: "workflow", Repository: repo, ReachKnown: true,
			})
			for s := range 3 {
				g.Edges = append(g.Edges, store.GraphEdge{
					Source: wf,
					Target: fmt.Sprintf("vendor/action%02d", (r*6+w*3+s)%actions),
					Kind:   "step-uses-action", Refs: []string{"v1"}, Count: 1,
				})
			}
		}
	}
	dot, err := graphexport.DOT(g, graphexport.Options{GroupBy: "repository"})
	if err != nil {
		t.Fatalf("encoding the fixture: %v", err)
	}
	return dot
}

// Group boxes must not cross, or containment becomes ambiguous.
func TestTheDefaultEngineKeepsGroupBoundariesApart(t *testing.T) {
	requireGraphviz(t, Default)

	result, err := Compute(context.Background(), fannedDOT(t), Options{Engine: Default})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(result.Clusters) < 2 {
		t.Fatalf("the fixture came back with %d boundaries, too few to cross",
			len(result.Clusters))
	}
	for i, a := range result.Clusters {
		for _, b := range result.Clusters[i+1:] {
			if a.X1 < b.X2 && b.X1 < a.X2 && a.Y1 < b.Y2 && b.Y1 < a.Y2 {
				t.Errorf("%s drew %s across %s, so a node sits in both", Default, a.Name, b.Name)
			}
		}
	}
}

// The default engine must draw the group boundaries the interface offers.
func TestTheDefaultEngineDrawsTheBoundariesAGroupingPromises(t *testing.T) {
	if !SupportsClusters(Default) {
		t.Errorf("the default engine %s does not lay clusters out", Default)
	}
}

// Available reports whether an engine can be run at all.
func Available(e Engine) bool {
	program, known := programFor(e)
	if !known {
		return false
	}
	_, err := exec.LookPath(program)
	return err == nil
}

// The version is read from the installed Graphviz, and it is what a cached figure and its caption
// are keyed on: a drawing is byte-identical only within one version.
func TestTheInstalledVersionIsReadFromGraphviz(t *testing.T) {
	requireGraphviz(t, Dot)
	version, err := Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version == "" || strings.ContainsAny(version, " \n") || !strings.Contains(version, ".") {
		t.Errorf("Version() = %q, want a bare dotted version", version)
	}
}

func TestAVersionLineIsReadWhateverPrecedesIt(t *testing.T) {
	for line, want := range map[string]string{
		"dot - graphviz version 16.1.0 (20260904.0139)\n": "16.1.0",
		"dot - graphviz version 2.43.0 (0)\n":             "2.43.0",
		"dot - Graphviz version 9.0.0 (20230911.1827)":    "9.0.0",
	} {
		got, err := parseVersion(line)
		if err != nil || got != want {
			t.Errorf("parseVersion(%q) = %q, %v; want %q", line, got, err, want)
		}
	}
	if _, err := parseVersion("dot: command not found"); err == nil {
		t.Error("a line naming no version was read as one")
	}
}

// A server without Graphviz is a broken installation, and the error says how to fix it.
func TestAMissingGraphvizNamesTheRemedy(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Require(context.Background())
	if err == nil {
		t.Fatal("Require succeeded with no dot on the PATH")
	}
	if !ErrUnavailable(err) || !strings.Contains(err.Error(), "install Graphviz") {
		t.Errorf("Require() = %v, want ErrUnavailable naming the remedy", err)
	}
}

// A drawing needs each box's size and each edge's route, not only where the centers went.
func TestBoxesAndRoutesComeBackForEveryNodeAndEdge(t *testing.T) {
	requireGraphviz(t, Dot)
	doc := []byte(`digraph g { newrank=true; rankdir=LR;
	  node [shape=box, fixedsize=true, label=""];
	  "a" [width=1.5, height=0.5]; "b@v1" [width=2, height=0.75];
	  "a" -> "b@v1"; }`)
	result, err := Compute(context.Background(), doc, Options{Engine: Dot})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	boxes := map[string]Box{}
	for _, b := range result.Boxes {
		boxes[b.Key] = b
	}
	if a := boxes["a"]; a.W != 108 || a.H != 36 {
		t.Errorf("a came back %v x %v points, want the 108 x 36 it was given", a.W, a.H)
	}
	if b := boxes["b@v1"]; b.W != 144 || b.H != 54 {
		t.Errorf("b@v1 came back %v x %v points, want 144 x 54", b.W, b.H)
	}
	if len(result.Splines) != 1 {
		t.Fatalf("%d routes for one edge", len(result.Splines))
	}
	route := result.Splines[0]
	if route.Tail != "a" || route.Head != "b@v1" {
		t.Errorf("the route runs %s -> %s, want a -> b@v1", route.Tail, route.Head)
	}
	if len(route.Points) < 4 || (len(route.Points)-1)%3 != 0 || route.Arrow == nil {
		t.Errorf("the route has %d points and arrow %v; want a cubic B-spline and its arrow tip",
			len(route.Points), route.Arrow)
	}
	if boxes["b@v1"].X <= boxes["a"].X {
		t.Error("left to right put the head of the edge left of its tail")
	}
}
