package figure

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A figure at the cap still places: no overlapping boxes, callers on one side of the page node
// and callees on the other. The search that chose the cap placed 300 of each shape in about 50ms.
func TestAFigureAtTheCapKeepsItsSidesAndDoesNotOverlap(t *testing.T) {
	requireGraphviz(t)
	for _, shape := range []string{"fan-in", "fan-out", "mixed"} {
		t.Run(shape, func(t *testing.T) {
			c := shaped(DefaultLimit, shape)
			p, err := Place(context.Background(), c, Options{})
			if err != nil {
				t.Fatalf("placing %s at the cap: %v", shape, err)
			}
			if len(p.boxes) != DefaultLimit {
				t.Fatalf("placed %d of %d boxes", len(p.boxes), DefaultLimit)
			}
			var rootX float64
			found := false
			for _, n := range c.Nodes {
				if n.Side == SideRoot {
					rootX = p.boxes[n.Key].X
					found = true
					break
				}
			}
			if !found {
				t.Fatal("the shape has no page node")
			}
			keys := make([]string, 0, len(p.boxes))
			for k := range p.boxes {
				keys = append(keys, k)
			}
			for i, a := range keys {
				boxA := p.boxes[a]
				for _, b := range keys[i+1:] {
					boxB := p.boxes[b]
					if math.Abs(boxA.X-boxB.X) < (boxA.W+boxB.W)/2 && math.Abs(boxA.Y-boxB.Y) < (boxA.H+boxB.H)/2 {
						t.Errorf("%s and %s overlap", a, b)
					}
				}
			}
			for _, n := range c.Nodes {
				b := p.boxes[n.Key]
				switch n.Side {
				case SideCaller:
					if b.X >= rootX {
						t.Errorf("caller %s sits at x=%v, not left of %v", n.Key, b.X, rootX)
					}
				case SideDependency:
					if b.X <= rootX {
						t.Errorf("callee %s sits at x=%v, not right of %v", n.Key, b.X, rootX)
					}
				}
			}
		})
	}
}

func shaped(n int, shape string) Closure {
	c := Closure{Key: "page", Nodes: []Node{}, Edges: []Edge{}}
	switch shape {
	case "fan-out":
		root := "org/app/.github/workflows/ci.yml"
		c.Nodes = append(c.Nodes, Node{GraphNode: store.GraphNode{Key: root, Kind: "workflow", Repository: "org/app"}, Side: SideRoot})
		for i := 1; i < n; i++ {
			key := fmt.Sprintf("actions/tool-%03d@v1", i)
			c.Nodes = append(c.Nodes, Node{GraphNode: store.GraphNode{Key: key, Kind: "action", Ref: "v1", Owner: "actions"}, Side: SideDependency})
			c.Edges = append(c.Edges, Edge{Source: root, Target: key})
		}
	case "mixed":
		root := "org/app/.github/actions/setup"
		c.Nodes = append(c.Nodes, Node{GraphNode: store.GraphNode{Key: root, Kind: "composite_action", Repository: "org/app"}, Side: SideRoot})
		callers := (n - 1) / 2
		for i := 0; i < callers; i++ {
			key := fmt.Sprintf("org/repo-%03d/.github/workflows/build.yml", i)
			c.Nodes = append(c.Nodes, Node{GraphNode: store.GraphNode{Key: key, Kind: "workflow", Repository: fmt.Sprintf("org/repo-%03d", i)}, Side: SideCaller})
			c.Edges = append(c.Edges, Edge{Source: key, Target: root})
		}
		for i := callers + 1; i < n; i++ {
			key := fmt.Sprintf("actions/tool-%03d@v1", i)
			c.Nodes = append(c.Nodes, Node{GraphNode: store.GraphNode{Key: key, Kind: "action", Ref: "v1"}, Side: SideDependency})
			c.Edges = append(c.Edges, Edge{Source: root, Target: key})
		}
	default:
		versions := min(7, n)
		var roots []string
		for i := 0; i < versions; i++ {
			ref := fmt.Sprintf("v%d", i+1)
			key := "actions/checkout@" + ref
			roots = append(roots, key)
			c.Nodes = append(c.Nodes, Node{GraphNode: store.GraphNode{Key: key, Kind: "action", Ref: ref, Owner: "actions"}, Side: SideRoot})
		}
		for i := versions; i < n; i++ {
			key := fmt.Sprintf("org/repo-%03d/.github/workflows/build.yml", i)
			c.Nodes = append(c.Nodes, Node{GraphNode: store.GraphNode{Key: key, Kind: "workflow", Repository: fmt.Sprintf("org/repo-%03d", i)}, Side: SideCaller})
			c.Edges = append(c.Edges, Edge{Source: key, Target: roots[i%versions]})
		}
	}
	return c
}
