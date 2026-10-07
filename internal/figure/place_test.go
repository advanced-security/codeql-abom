package figure

import (
	"bytes"
	"context"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/layout"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func requireGraphviz(t *testing.T) {
	t.Helper()
	if _, err := layout.Require(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// ciClosure is a workflow page's figure: two callers of a reusable workflow, which runs an
// action at two versions and a composite action that runs another.
func ciClosure(t *testing.T) Closure {
	t.Helper()
	g := graphOf([]store.GraphNode{
		{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow", Repository: "octo/app", Owned: true},
		{Key: "octo/web/.github/workflows/ci.yml", Kind: "workflow", Repository: "octo/web", Owned: true},
		{Key: "octo/shared/.github/workflows/build.yml", Kind: "reusable_workflow",
			Repository: "octo/shared", Owned: true, Findings: 1,
			Facets: []store.FindingFacet{{Rule: "r", Band: "medium", Count: 1}}},
		{Key: "actions/checkout@v4", Kind: "action", Owner: "actions", Ref: "v4", IsExternal: true, Analyzed: true},
		{Key: "actions/checkout@v5", Kind: "action", Owner: "actions", Ref: "v5", IsExternal: true},
		{Key: "octo/shared/.github/actions/setup", Kind: "composite_action", Repository: "octo/shared", Owned: true},
		{Key: "acme/lint@v2", Kind: "action", Owner: "acme", Ref: "v2", IsExternal: true, Analyzed: true,
			Advisories: 2},
	},
		"octo/app/.github/workflows/ci.yml>octo/shared/.github/workflows/build.yml",
		"octo/web/.github/workflows/ci.yml>octo/shared/.github/workflows/build.yml",
		"octo/shared/.github/workflows/build.yml>actions/checkout@v4",
		"octo/shared/.github/workflows/build.yml>actions/checkout@v5",
		"octo/shared/.github/workflows/build.yml>octo/shared/.github/actions/setup",
		"octo/shared/.github/actions/setup>acme/lint@v2",
	)
	return Cut(g, "octo/shared/.github/workflows/build.yml", CutOptions{})
}

// The document is the same bytes whatever order the projection arrived in, or a cache keyed on it
// would miss and a figure would move.
func TestTheDocumentIsTheSameWhateverOrderTheClosureArrivedIn(t *testing.T) {
	c := ciClosure(t)
	want := Document(c, Options{})
	if got := bytes.Count(want, []byte("[width=")); got != len(c.Nodes) {
		t.Fatalf("the document sizes %d boxes for %d nodes:\n%s", got, len(c.Nodes), want)
	}
	for i := range 10 {
		shuffled := c
		shuffled.Nodes = slices.Clone(c.Nodes)
		shuffled.Edges = slices.Clone(c.Edges)
		r := rand.New(rand.NewPCG(uint64(i), 7))
		r.Shuffle(len(shuffled.Nodes), func(a, b int) { shuffled.Nodes[a], shuffled.Nodes[b] = shuffled.Nodes[b], shuffled.Nodes[a] })
		r.Shuffle(len(shuffled.Edges), func(a, b int) { shuffled.Edges[a], shuffled.Edges[b] = shuffled.Edges[b], shuffled.Edges[a] })
		if got := Document(shuffled, Options{}); !bytes.Equal(got, want) {
			t.Fatalf("shuffle %d wrote a different document:\n%s\nwant\n%s", i, got, want)
		}
	}
}

// Graphviz sizes nothing: every box is the size ABOM's own label needs, so what is placed is what
// is drawn.
func TestEveryBoxIsTheSizeItsLabelNeeds(t *testing.T) {
	short := labelOf(Node{GraphNode: store.GraphNode{Key: "a/b", Kind: "action"}})
	long := labelOf(Node{GraphNode: store.GraphNode{Key: "a/a-very-long-action-name-indeed", Kind: "action"}})
	if long.width <= short.width {
		t.Errorf("a longer name got a box %v wide against %v", long.width, short.width)
	}
	doc := string(Document(ciClosure(t), Options{}))
	if !strings.Contains(doc, "fixedsize=true") {
		t.Error("the document lets Graphviz size the boxes")
	}
}

// Keys come from files ABOM did not write, and DOT has no quoted form for some of them. None is
// written into the document, and each comes back placed.
func TestHostileKeysSurviveGraphvizUnchanged(t *testing.T) {
	requireGraphviz(t)
	hostile := []string{`o/a"b`, `o/back\`, `o/<tag>&amp;`, `o/#comment`, `o/with space`, `o/x\"y`}
	var uses []string
	for _, k := range hostile {
		uses = append(uses, k+">r/act")
	}
	c := Cut(graphOf(nil, uses...), "r/act", CutOptions{})
	placed, err := Place(context.Background(), c, Options{})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	doc := Document(c, Options{})
	for _, k := range hostile {
		if _, ok := placed.boxes[k]; !ok {
			t.Errorf("%q did not come back from Graphviz", k)
		}
		if bytes.Contains(doc, []byte(k)) {
			t.Errorf("%q was written into the document Graphviz parses", k)
		}
	}
}

func TestTheSameClosureIsPlacedIdenticallyTwice(t *testing.T) {
	requireGraphviz(t)
	first, err := Place(context.Background(), ciClosure(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Place(context.Background(), ciClosure(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.boxes) != len(ciClosure(t).Nodes) {
		t.Fatalf("placed %d of %d nodes", len(first.boxes), len(ciClosure(t).Nodes))
	}
	if !placementsEqual(first, second) {
		t.Error("one closure was placed two ways")
	}
}

func placementsEqual(a, b Placement) bool {
	if a.width != b.width || a.height != b.height || len(a.boxes) != len(b.boxes) || len(a.routes) != len(b.routes) {
		return false
	}
	for k, box := range a.boxes {
		if b.boxes[k] != box {
			return false
		}
	}
	for i := range a.routes {
		if !slices.Equal(a.routes[i].Points, b.routes[i].Points) {
			return false
		}
	}
	return true
}

// What runs the node sits left of it and what it runs right of it; nothing is unplaced, no two
// boxes overlap, and every route ends on the boxes it joins.
func TestThePlacementReadsLeftToRightWithoutOverlap(t *testing.T) {
	requireGraphviz(t)
	c := ciClosure(t)
	p, err := Place(context.Background(), c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	root := p.boxes["octo/shared/.github/workflows/build.yml"]
	for _, n := range c.Nodes {
		b, ok := p.boxes[n.Key]
		if !ok {
			t.Errorf("%s was not placed", n.Key)
			continue
		}
		switch n.Side {
		case SideCaller:
			if b.X >= root.X {
				t.Errorf("caller %s sits at x=%v, not left of the node at %v", n.Key, b.X, root.X)
			}
		case SideDependency:
			if b.X <= root.X {
				t.Errorf("dependency %s sits at x=%v, not right of the node at %v", n.Key, b.X, root.X)
			}
		}
	}
	keys := slices.Sorted(func(yield func(string) bool) {
		for k := range p.boxes {
			if !yield(k) {
				return
			}
		}
	})
	for i, a := range keys {
		for _, b := range keys[i+1:] {
			if overlap(p.boxes[a], p.boxes[b]) {
				t.Errorf("%s and %s overlap", a, b)
			}
		}
	}
	if len(p.routes) != len(c.Edges) {
		t.Fatalf("%d routes for %d edges", len(p.routes), len(c.Edges))
	}
	for _, r := range p.routes {
		start, end := r.Points[0], r.Points[len(r.Points)-1]
		if r.Arrow != nil {
			end = *r.Arrow
		}
		if !touches(p.boxes[r.Tail], start) || !touches(p.boxes[r.Head], end) {
			t.Errorf("the route %s -> %s does not end on its boxes", r.Tail, r.Head)
		}
	}
}

// Top to bottom puts what runs the node above it and what it runs below it.
func TestTopToBottomPutsCallersAboveAndDependenciesBelow(t *testing.T) {
	requireGraphviz(t)
	c := ciClosure(t)
	p, err := Place(context.Background(), c, Options{Direction: TopToBottom})
	if err != nil {
		t.Fatal(err)
	}
	root := p.boxes["octo/shared/.github/workflows/build.yml"]
	for _, n := range c.Nodes {
		b := p.boxes[n.Key]
		if n.Side == SideCaller && b.Y >= root.Y || n.Side == SideDependency && b.Y <= root.Y {
			t.Errorf("%s (%s) sits at y=%v against the node's %v", n.Key, n.Side, b.Y, root.Y)
		}
	}
}

func overlap(a, b box) bool {
	return math.Abs(a.X-b.X) < (a.W+b.W)/2 && math.Abs(a.Y-b.Y) < (a.H+b.H)/2
}

// touches allows the few points Graphviz leaves between a box and the end of its route.
func touches(b box, p layout.Point) bool {
	const slack = 3
	return p.X >= b.X-b.W/2-slack && p.X <= b.X+b.W/2+slack && p.Y >= b.Y-b.H/2-slack && p.Y <= b.Y+b.H/2+slack
}

// No account box: holding every action of one account in one box spanning three ranks drew
// swift.yml's edges across the whole figure. A label carries its account instead.
func TestALabelSaysWhereItLivesWithoutAnAccountBox(t *testing.T) {
	for _, tc := range []struct {
		n             store.GraphNode
		name, context string
	}{
		{store.GraphNode{Key: "actions/checkout@v4", Kind: "action", Ref: "v4"}, "actions/checkout@v4", "action"},
		{store.GraphNode{Key: "github/codeql-action/init@v3", Kind: "action", Ref: "v3"}, "init@v3", "github/codeql-action"},
		{store.GraphNode{Key: "o/r/.github/workflows/ci.yml", Kind: "workflow", Repository: "o/r"}, "ci.yml", "o/r"},
		{store.GraphNode{Key: "o/r/.github/actions/setup", Kind: "composite_action", Repository: "o/r"}, "setup", "o/r"},
	} {
		name, context := nameAndContext(tc.n)
		if name != tc.name || context != tc.context {
			t.Errorf("%s reads %q / %q, want %q / %q", tc.n.Key, name, context, tc.name, tc.context)
		}
	}
	if doc := Document(ciClosure(t), Options{}); bytes.Contains(doc, []byte("cluster")) {
		t.Errorf("the document draws account boxes:\n%s", doc)
	}
}
