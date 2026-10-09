// Package figure draws what runs one node and what it runs, the same way every time.
//
// The closure is cut from the Explorer's projection, so the figure and the Explorer name the same
// nodes; Graphviz places it, and this package writes the drawing.
package figure

import (
	"cmp"
	"slices"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
)

const (
	// DefaultLimit is the most nodes one figure draws. 300 is the largest size the placement
	// search held: fan-in, fan-out and mixed, no overlapping boxes, callers and callees on their
	// own sides, in about 50ms. A larger closure is cut before dot runs, and the page says so.
	DefaultLimit = 300
	// DefaultMaxVersions bounds the page's own versions, so a key used at many leaves room for
	// what runs them.
	DefaultMaxVersions = 12
)

// Side is where a node sits relative to the page's node.
type Side string

const (
	SideRoot       Side = "root"
	SideCaller     Side = "caller"
	SideDependency Side = "dependency"
)

// Node is one drawn node: the projection's own facts, which side it is on, and how far.
type Node struct {
	store.GraphNode
	Side Side `json:"side"`
	// Distance counts the relationships between it and the nearest version of the page's node.
	Distance int `json:"distance"`
}

// Edge is one drawn relationship, merged across kinds and references between the same pair.
type Edge struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Kind   string   `json:"kind"`
	Refs   []string `json:"refs"`
	// Route is set where the edge leads, within the drawing, to a finding or an advisory.
	Route bool `json:"route"`
}

// Counts is what the figure drew of the closure the page's terms admit, and what it left out.
type Counts struct {
	Drawn int `json:"drawn"`
	// InClosure counts every node of the directed closure, every version of the page's node
	// included, before the limit.
	InClosure int `json:"inClosure"`
	LeftOut   int `json:"leftOut"`
	// Versions counts the page's own versions in use, VersionsDrawn those drawn.
	Versions      int `json:"versions"`
	VersionsDrawn int `json:"versionsDrawn"`
	Callers       int `json:"callers"`
	Dependencies  int `json:"dependencies"`
	// NotListed counts what runs it that the page's terms do not list, which is never drawn.
	NotListed int `json:"notListed"`
}

// Closure is what one figure draws.
type Closure struct {
	Key    string `json:"key"`
	Nodes  []Node `json:"nodes"`
	Edges  []Edge `json:"edges"`
	Counts Counts `json:"counts"`
}

// CutOptions narrows the closure the way the page's own sections are narrowed.
type CutOptions struct {
	// At narrows the page's own versions to these references.
	At []string
	// Callers names, by key without a version, the workflows and actions the routes the page lists
	// start from. Nil draws everything that runs the node.
	Callers map[string]bool
	// Limit and MaxVersions default to DefaultLimit and DefaultMaxVersions.
	Limit       int
	MaxVersions int
}

// usesKinds are the relationships a closure walks: what a workflow or action runs. Containment is
// a drawing aid, and ordering between jobs is not use.
var usesKinds = map[string]bool{
	store.EdgeKindStepUsesAction:          true,
	store.EdgeKindJobUsesReusableWorkflow: true,
	store.EdgeKindCompositeStepUsesAction: true,
}

// BaseKey is a node's key without the version it was split by.
func BaseKey(n store.GraphNode) string {
	if n.Ref != "" {
		if base, ok := strings.CutSuffix(n.Key, "@"+n.Ref); ok {
			return base
		}
	}
	return n.Key
}

// Cut takes the closure of one key from a projection: what runs it and what it runs, each walked
// directed to any depth, and at most the limit of it, chosen by distance with what you own first.
func Cut(g store.Graph, key string, opts CutOptions) Closure {
	limit := cmp.Or(opts.Limit, DefaultLimit)
	maxVersions := min(cmp.Or(opts.MaxVersions, DefaultMaxVersions), limit)

	nodes := make(map[string]store.GraphNode, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes[n.Key] = n
	}
	edges := mergedUses(g.Edges, nodes)
	out, in := adjacency(edges)

	roots := versionsOf(g.Nodes, key, opts.At)
	everything := walk(roots, out, in, nodes, opts.Callers)
	notListed := everything.notListed
	// A version only what the page's terms leave out runs is not in use under them.
	if opts.Callers != nil && len(everything.run) > 0 && len(everything.run) < len(roots) {
		roots = slices.DeleteFunc(roots, func(r string) bool { return !everything.run[r] })
		everything = walk(roots, out, in, nodes, opts.Callers)
	}
	drawnRoots := roots[:min(len(roots), maxVersions)]
	w := walk(drawnRoots, out, in, nodes, opts.Callers)

	c := Closure{Key: key, Nodes: []Node{}, Edges: []Edge{}}
	c.Counts.Versions, c.Counts.VersionsDrawn = len(roots), len(drawnRoots)
	c.Counts.NotListed = notListed
	for _, side := range everything.side {
		switch side {
		case SideCaller:
			c.Counts.Callers++
		case SideDependency:
			c.Counts.Dependencies++
		}
	}
	c.Counts.InClosure = len(roots) + c.Counts.Callers + c.Counts.Dependencies

	chosen := choose(drawnRoots, w, nodes, limit)
	for k := range chosen {
		c.Nodes = append(c.Nodes, Node{GraphNode: nodes[k], Side: w.side[k], Distance: w.dist[k]})
	}
	slices.SortFunc(c.Nodes, func(a, b Node) int { return cmp.Compare(a.Key, b.Key) })
	for _, e := range edges {
		if chosen[e.Source] && chosen[e.Target] && onPath(w.side[e.Source], w.side[e.Target]) {
			c.Edges = append(c.Edges, e)
		}
	}
	markRoutes(c.Edges, nodes)
	c.Counts.Drawn = len(c.Nodes)
	c.Counts.LeftOut = c.Counts.InClosure - c.Counts.Drawn
	return c
}

// mergedUses is one edge per pair the closure can walk, its references joined and sorted.
func mergedUses(in []store.GraphEdge, nodes map[string]store.GraphNode) []Edge {
	byPair := map[[2]string]*Edge{}
	for _, e := range in {
		if !usesKinds[e.Kind] || e.Source == e.Target {
			continue
		}
		if _, ok := nodes[e.Source]; !ok {
			continue
		}
		if _, ok := nodes[e.Target]; !ok {
			continue
		}
		pair := [2]string{e.Source, e.Target}
		merged, ok := byPair[pair]
		if !ok {
			merged = &Edge{Source: e.Source, Target: e.Target, Kind: e.Kind, Refs: []string{}}
			byPair[pair] = merged
		}
		merged.Kind = min(merged.Kind, e.Kind)
		merged.Refs = append(merged.Refs, e.Refs...)
	}
	out := make([]Edge, 0, len(byPair))
	for _, e := range byPair {
		slices.Sort(e.Refs)
		e.Refs = slices.Compact(e.Refs)
		out = append(out, *e)
	}
	slices.SortFunc(out, func(a, b Edge) int {
		return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.Target, b.Target))
	})
	return out
}

func adjacency(edges []Edge) (out, in map[string][]string) {
	out, in = map[string][]string{}, map[string][]string{}
	for _, e := range edges {
		out[e.Source] = append(out[e.Source], e.Target)
		in[e.Target] = append(in[e.Target], e.Source)
	}
	return out, in
}

// versionsOf is the page's own node once per version in use, widest reach first. `at` narrows
// them where the node has versions; one that has none is drawn as itself.
func versionsOf(all []store.GraphNode, key string, at []string) []string {
	var found []store.GraphNode
	versioned := false
	for _, n := range all {
		if n.Kind == store.OrgNodeKind || n.Kind == "repo" || BaseKey(n) != key {
			continue
		}
		found = append(found, n)
		versioned = versioned || n.Ref != ""
	}
	if versioned && len(at) > 0 {
		found = slices.DeleteFunc(found, func(n store.GraphNode) bool { return !slices.Contains(at, n.Ref) })
	}
	slices.SortFunc(found, func(a, b store.GraphNode) int {
		return cmp.Or(cmp.Compare(b.DependentRepos, a.DependentRepos), cmp.Compare(a.Ref, b.Ref),
			cmp.Compare(a.Key, b.Key))
	})
	out := make([]string, len(found))
	for i, n := range found {
		out[i] = n.Key
	}
	return out
}

// walked is one pair of directed walks: each node's side, distance and the neighbor that leads
// back to a version of the page's node.
type walked struct {
	side      map[string]Side
	dist      map[string]int
	parent    map[string]string
	notListed int
	// run holds the versions something listed runs, when the page's terms list who runs it.
	run map[string]bool
}

// walk follows what the roots run, then what runs them. A node reached both ways is a dependency:
// a route that runs it and is run by it is a cycle, which no workflow can execute.
func walk(roots []string, out, in map[string][]string, nodes map[string]store.GraphNode,
	callers map[string]bool) walked {
	w := walked{side: map[string]Side{}, dist: map[string]int{}, parent: map[string]string{}}
	for _, r := range roots {
		w.side[r], w.dist[r] = SideRoot, 0
	}
	w.breadthFirst(roots, out, SideDependency)
	w.breadthFirst(roots, in, SideCaller)
	if callers == nil {
		return w
	}
	// Every route from a listed start: what it runs on the way to the page's node, which includes
	// each start's own path back.
	listed := map[string]bool{}
	w.run = map[string]bool{}
	var queue []string
	for _, k := range sortedKeys(w.side) {
		if w.side[k] == SideCaller && callers[BaseKey(nodes[k])] {
			queue = append(queue, k)
		}
	}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if listed[k] {
			continue
		}
		listed[k] = true
		for _, next := range out[k] {
			switch w.side[next] {
			case SideCaller:
				queue = append(queue, next)
			case SideRoot:
				w.run[next] = true
			}
		}
	}
	for k, side := range w.side {
		if side == SideCaller && !listed[k] {
			delete(w.side, k)
			delete(w.dist, k)
			delete(w.parent, k)
			w.notListed++
		}
	}
	return w
}

// rootOf is the version of the page's node a node's path leads back to.
func (w walked) rootOf(k string) string {
	for w.side[k] != SideRoot {
		k = w.parent[k]
	}
	return k
}

// breadthFirst walks one direction level by level in key order, so the parent each node keeps is
// the same on every run.
func (w *walked) breadthFirst(roots []string, next map[string][]string, side Side) {
	level := slices.Sorted(slices.Values(roots))
	for d := 1; len(level) > 0; d++ {
		var found []string
		for _, from := range level {
			for _, to := range slices.Sorted(slices.Values(next[from])) {
				if _, seen := w.side[to]; seen {
					continue
				}
				w.side[to], w.dist[to], w.parent[to] = side, d, from
				found = append(found, to)
			}
		}
		slices.Sort(found)
		level = found
	}
}

// choose takes the roots, then what you own nearest first, then everything else nearest first,
// each with its path back to a root. A node whose path no longer fits is skipped.
func choose(roots []string, w walked, nodes map[string]store.GraphNode, limit int) map[string]bool {
	chosen := map[string]bool{}
	for _, r := range roots {
		chosen[r] = true
	}
	var rest []string
	for k, side := range w.side {
		if side != SideRoot {
			rest = append(rest, k)
		}
	}
	class := func(a, b string) int {
		return cmp.Or(cmpOwned(nodes[a].Owned, nodes[b].Owned), cmp.Compare(w.dist[a], w.dist[b]))
	}
	slices.SortFunc(rest, func(a, b string) int { return cmp.Or(class(a, b), cmp.Compare(a, b)) })
	// Within one class, each version's first candidate before any version's second, so a page
	// with many versions draws who runs each of them rather than everyone who runs one.
	turn, taken := map[string]int{}, map[[3]any]int{}
	for _, k := range rest {
		group := [3]any{nodes[k].Owned, w.dist[k], w.rootOf(k)}
		turn[k] = taken[group]
		taken[group]++
	}
	slices.SortStableFunc(rest, func(a, b string) int { return cmp.Or(class(a, b), cmp.Compare(turn[a], turn[b])) })
	for _, k := range rest {
		var path []string
		for at := k; !chosen[at]; at = w.parent[at] {
			path = append(path, at)
		}
		if len(chosen)+len(path) > limit {
			continue
		}
		for _, p := range path {
			chosen[p] = true
		}
	}
	return chosen
}

// cmpOwned orders what you own first.
func cmpOwned(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return -1
	}
	return 1
}

// onPath reports whether an edge between these sides lies on a directed path through the page's
// node: into a root or a caller from a caller, or out of a root or a dependency to a dependency.
func onPath(from, to Side) bool {
	return from == SideCaller && (to == SideCaller || to == SideRoot) ||
		(from == SideRoot || from == SideDependency) && to == SideDependency
}

// markRoutes emphasizes each edge leading, within the drawing, to a node carrying a finding or an
// advisory.
func markRoutes(edges []Edge, nodes map[string]store.GraphNode) {
	out := map[string][]string{}
	for _, e := range edges {
		out[e.Source] = append(out[e.Source], e.Target)
	}
	ahead := map[string]bool{}
	visiting := map[string]bool{}
	var leads func(string) bool
	leads = func(k string) bool {
		if v, done := ahead[k]; done {
			return v
		}
		if visiting[k] {
			return false
		}
		visiting[k] = true
		n := nodes[k]
		found := n.Findings > 0 || n.Advisories > 0
		for _, next := range out[k] {
			found = leads(next) || found
		}
		ahead[k] = found
		return found
	}
	for i := range edges {
		edges[i].Route = leads(edges[i].Target)
	}
}

// sortedKeys is a map's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
