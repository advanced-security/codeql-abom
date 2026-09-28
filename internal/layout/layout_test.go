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

func requireGraphviz(t *testing.T, e Engine) {
	t.Helper()
	if !Available(e) {
		t.Skipf("%s is not installed; layout is optional and the interface degrades without it", e)
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
