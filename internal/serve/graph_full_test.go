package serve

import "testing"

// The whole-corpus payload is what the Explorer loads once and filters in the browser, so a
// missing field there is invisible until a filter silently matches nothing.
func TestFullGraphCarriesEverythingTheInterfaceFiltersOn(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	type node struct {
		Key       string   `json:"key"`
		Kind      string   `json:"kind"`
		Owner     string   `json:"owner"`
		Rules     []string `json:"rules"`
		Container string   `json:"container"`
	}
	type edge struct {
		Source string   `json:"source"`
		Target string   `json:"target"`
		Refs   []string `json:"refs"`
	}
	type graph struct {
		Nodes         []node `json:"nodes"`
		Edges         []edge `json:"edges"`
		IncludesSteps bool   `json:"includesSteps"`
	}

	g := decode[graph](t, get(t, srv, "/api/graph/full"))
	if len(g.Nodes) == 0 {
		t.Fatal("no nodes in the whole-corpus payload")
	}
	for _, n := range g.Nodes {
		if n.Rules == nil {
			t.Errorf("%s carries a null rule list; the rule filter reads it as a set", n.Key)
		}
	}
	for _, e := range g.Edges {
		if e.Refs == nil {
			t.Errorf("edge %s -> %s carries a null reference list", e.Source, e.Target)
		}
	}
}

// An owner with no edges renders as an island and drilling into it finds nothing, which is
// exactly what happened to repositories before containment edges were emitted for them.
func TestOwnerNodesReachTheGraphTheyDescribe(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	type node struct {
		Key  string `json:"key"`
		Kind string `json:"kind"`
	}
	type edge struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}
	type graph struct {
		Nodes []node `json:"nodes"`
		Edges []edge `json:"edges"`
	}

	g := decode[graph](t, get(t, srv, "/api/graph/full"))

	attached := map[string]bool{}
	for _, e := range g.Edges {
		attached[e.Source] = true
		attached[e.Target] = true
	}

	var owners int
	for _, n := range g.Nodes {
		if n.Kind != "org" {
			continue
		}
		owners++
		if !attached[n.Key] {
			t.Errorf("owner %s has no edges", n.Key)
		}
	}
	if owners == 0 {
		t.Fatal("no owner nodes derived for a corpus with repositories and external actions")
	}
}

// Steps exist only in the expanded payload. Asking for them and getting the collapsed one would
// make the step filter appear broken rather than absent.
func TestExpandedPayloadIsOptedIntoExplicitly(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	type graph struct {
		Nodes []struct {
			Kind string `json:"kind"`
		} `json:"nodes"`
		IncludesSteps bool `json:"includesSteps"`
	}

	collapsed := decode[graph](t, get(t, srv, "/api/graph/full"))
	if collapsed.IncludesSteps {
		t.Error("the default payload claims to include steps")
	}
	for _, n := range collapsed.Nodes {
		if n.Kind == "step" {
			t.Error("a step survived into the collapsed payload")
		}
	}

	expanded := decode[graph](t, get(t, srv, "/api/graph/full?steps=true"))
	if !expanded.IncludesSteps {
		t.Error("the expanded payload does not say so")
	}
	var steps int
	for _, n := range expanded.Nodes {
		if n.Kind == "step" {
			steps++
		}
	}
	if steps == 0 {
		t.Error("asked for steps and got none")
	}
}
