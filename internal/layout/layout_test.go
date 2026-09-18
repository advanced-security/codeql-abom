package layout

import (
	"context"
	"fmt"
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

func requireGraphviz(t *testing.T, e Engine) {
	t.Helper()
	if !Available(e) {
		t.Skipf("%s is not installed; layout is optional and the interface degrades without it", e)
	}
}

// The whole reason for laying out on the server is that a picture can be depended on. An engine
// that placed nodes differently each run would leave the interface exactly where it started.
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

// A force engine seeds itself from the clock unless told not to, so the one thing this is for --
// the same graph drawing the same picture -- is lost by default.
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

// The engine name selects a binary to execute. Anything outside the known set is refused before
// a process is started, never passed through as a program to run.
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

// The boundary is the point: a cluster is drawn as a box around its members rather than a hull
// guessed afterwards, and the box has to reach the caller for anything to draw it.
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

// An engine given a pathological graph runs for as long as it takes. A request that cannot be
// abandoned is a request that holds a connection open until somebody restarts the server.
func TestLayoutStopsWhenTheCallerGivesUp(t *testing.T) {
	requireGraphviz(t, Dot)

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	if _, err := Compute(ctx, fixtureDOT(t, ""), Options{Engine: Dot}); err == nil {
		t.Error("a canceled layout still returned a result")
	}
}

// Graphviz is optional, so its absence is an answer rather than a fault -- but a silent one would
// leave a reader believing they were looking at a stable layout when they were not.
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

// Every node in the document has to come back, or the interface silently falls back to the force
// simulation for whichever ones went missing and draws half a picture from each.
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

// Which engines box a group is a property of Graphviz, not of this code, so it is measured rather
// than assumed -- and asserted here so an upgrade that changes it fails loudly instead of quietly
// drawing a grouped corpus with no boundaries.
func TestOnlySomeEnginesDrawAClusterBoundary(t *testing.T) {
	dot := fixtureDOT(t, "owner")
	for _, e := range Engines() {
		if !Available(e) {
			t.Skipf("%s is not installed", e)
		}
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

// fannedDOT is a corpus shape rather than a toy: several repositories whose workflows fan into a
// shared pool of actions. A broad rank of siblings is what a ranking engine spreads sideways, so
// this is the shape that exposes an engine's aspect ratio -- and the shape the Explorer holds.
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

// Two group boxes that cross put a node inside both of them, and a reader cannot tell which group
// it belongs to at any zoom. This is the property a default is chosen on rather than how square
// the canvas comes back: a wide canvas is still readable once it is scaled to fit, but crossed
// boundaries are not readable at any size.
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

// The interface offers to group, so the engine it asks for when nobody names one has to be able
// to draw a boundary. Defaulting to one that cannot would report every grouped corpus as ungrouped.
func TestTheDefaultEngineDrawsTheBoundariesAGroupingPromises(t *testing.T) {
	if !SupportsClusters(Default) {
		t.Errorf("the default engine %s does not lay clusters out", Default)
	}
}
