package figure

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/layout"
)

// Direction is which way the figure reads: what runs the node before it, what it runs after it.
type Direction string

const (
	// LeftToRight puts what runs the node on its left, for a page that is wide.
	LeftToRight Direction = "LR"
	// TopToBottom puts what runs the node above it, for a panel that is tall.
	TopToBottom Direction = "TB"
)

// Directions lists the directions a figure may be asked for.
func Directions() []Direction { return []Direction{LeftToRight, TopToBottom} }

// Options says how a figure is drawn, never what it holds.
type Options struct {
	Direction Direction
}

func (o Options) direction() Direction {
	if o.Direction == TopToBottom {
		return TopToBottom
	}
	return LeftToRight
}

// placeTimeout bounds one placement. A figure at DefaultLimit placed in about 50ms, so a run
// that is still going after 5s is a fault, not a reason to draw less.
const placeTimeout = 5 * time.Second

// document returns the graph and the key each node ID stands for. Keys come from files ABOM did not write,
// and DOT cannot quote every one of them: `\\` stays two characters, so a key ending in a backslash
// has no quoted form. Nothing from a key is written, so nothing needs one.
func document(c Closure, opts Options) ([]byte, []string) {
	var b strings.Builder
	b.WriteString("digraph \"figure\" {\n")
	// newrank keeps the versions' rank=same constraint global rather than per subgraph.
	fmt.Fprintf(&b, "  graph [newrank=true, rankdir=%s, nodesep=0.25, ranksep=0.6, splines=true, "+
		"pad=0, margin=0];\n", opts.direction())
	b.WriteString("  node [shape=box, fixedsize=true, label=\"\"];\n")
	b.WriteString("  edge [arrowsize=0.7];\n")

	nodes := slices.Clone(c.Nodes)
	slices.SortFunc(nodes, func(a, b Node) int { return strings.Compare(a.Key, b.Key) })
	byKey, ids, keys := map[string]Node{}, map[string]string{}, make([]string, len(nodes))
	for i, n := range nodes {
		byKey[n.Key], ids[n.Key], keys[i] = n, fmt.Sprintf("n%d", i), n.Key
	}
	var roots []string
	for _, n := range nodes {
		writeNode(&b, "  ", ids[n.Key], byKey[n.Key])
		if n.Side == SideRoot {
			roots = append(roots, ids[n.Key])
		}
	}
	// Every version of the page's node in one rank, however far its callers reach.
	if len(roots) > 1 {
		fmt.Fprintf(&b, "  { rank=same; %s; }\n", strings.Join(roots, "; "))
	}
	edges := slices.Clone(c.Edges)
	slices.SortFunc(edges, func(a, b Edge) int {
		if a.Source != b.Source {
			return strings.Compare(a.Source, b.Source)
		}
		return strings.Compare(a.Target, b.Target)
	})
	for _, e := range edges {
		fmt.Fprintf(&b, "  %s -> %s;\n", ids[e.Source], ids[e.Target])
	}
	b.WriteString("}\n")
	return []byte(b.String()), keys
}

func writeNode(b *strings.Builder, indent, id string, n Node) {
	l := labelOf(n)
	fmt.Fprintf(b, "%s%s [width=%.2f, height=%.2f];\n", indent, id, l.width/72, l.height/72)
}

// keyOf is the key a node ID stands for.
func keyOf(id string, keys []string) (string, bool) {
	var i int
	if _, err := fmt.Sscanf(id, "n%d", &i); err != nil || i < 0 || i >= len(keys) || fmt.Sprintf("n%d", i) != id {
		return "", false
	}
	return keys[i], true
}

// box is a placed node in the drawing's coordinates: the origin top left, X and Y its center.
type box struct{ X, Y, W, H float64 }

// Placement is where Graphviz put a closure, turned the way the drawing reads: y down.
type Placement struct {
	width, height float64
	boxes         map[string]box
	labels        map[string]label
	routes        []layout.Spline
}

// Place lays a closure out with Graphviz's dot.
func Place(ctx context.Context, c Closure, opts Options) (Placement, error) {
	doc, keys := document(c, opts)
	result, err := layout.Compute(ctx, doc, layout.Options{Engine: layout.Dot, Timeout: placeTimeout})
	if err != nil {
		return Placement{}, err
	}
	p := Placement{width: result.Width, height: result.Height, boxes: map[string]box{},
		labels: map[string]label{}}
	flip := func(pt layout.Point) layout.Point { return layout.Point{X: pt.X, Y: result.Height - pt.Y} }
	for _, n := range c.Nodes {
		p.labels[n.Key] = labelOf(n)
	}
	for _, b := range result.Boxes {
		key, ok := keyOf(b.Key, keys)
		if !ok {
			continue
		}
		l := p.labels[key]
		p.boxes[key] = box{X: b.X, Y: result.Height - b.Y, W: l.width, H: l.height}
	}
	if missing := len(c.Nodes) - len(p.boxes); missing > 0 {
		return Placement{}, fmt.Errorf("dot placed %d of %d nodes", len(p.boxes), len(c.Nodes))
	}
	for _, s := range result.Splines {
		tail, okTail := keyOf(s.Tail, keys)
		head, okHead := keyOf(s.Head, keys)
		if !okTail || !okHead {
			continue
		}
		route := layout.Spline{Tail: tail, Head: head}
		for _, pt := range s.Points {
			route.Points = append(route.Points, flip(pt))
		}
		if s.Arrow != nil {
			tip := flip(*s.Arrow)
			route.Arrow = &tip
		}
		p.routes = append(p.routes, route)
	}
	slices.SortFunc(p.routes, func(a, b layout.Spline) int {
		if a.Tail != b.Tail {
			return strings.Compare(a.Tail, b.Tail)
		}
		return strings.Compare(a.Head, b.Head)
	})
	return p, nil
}
