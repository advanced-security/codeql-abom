package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Traversal directions for a scoped view.
const (
	// DirectionDependents walks towards what depends on the focus: blast radius.
	DirectionDependents = "dependents"
	// DirectionDependencies walks towards what the focus pulls in.
	DirectionDependencies = "dependencies"
	// DirectionBoth walks both ways, which is what someone who has just selected a node means
	// by everything touching it.
	DirectionBoth = "both"
)

// Granularity levels for a scoped view.
const (
	// GranularityWorkflow shows the workflows and composite actions that hold the references.
	GranularityWorkflow = "workflow"
	// GranularityRepository folds everything a repository owns into one node.
	GranularityRepository = "repository"
)

// DefaultSubgraphNodes bounds a scoped view. It is a guard against a pathological scope, not a
// readability budget: the whole-corpus view has no cap at all.
const DefaultSubgraphNodes = 20000

// unboundedHops stands for no hop limit. The walk then ends when the graph is exhausted or the
// node budget stops it, and the node budget is the honest limit to state.
const unboundedHops = 1 << 30

// collapsedKinds are folded into the container that holds them. A reader recognizes a workflow;
// a step index is an implementation detail, and collapsing is what keeps a view inside budget.
var collapsedKinds = map[string]bool{"step": true, "job": true}

// SubgraphNode is one node in a scoped view.
type SubgraphNode struct {
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	Repository string `json:"repository"`
	// Origin distinguishes repositories under review from those fetched as dependencies.
	Origin     string `json:"origin"`
	IsExternal bool   `json:"isExternal"`
	Path       string `json:"path"`
	// Hops is the distance from the focus; the focus itself is zero.
	Hops           int `json:"hops"`
	DependentRepos int `json:"dependentRepos"`
	Findings       int `json:"findings"`
}

// SubgraphEdge is a dependency relationship, merged across the steps that expressed it.
type SubgraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	// Refs are the references observed for this relationship, verbatim and unjudged.
	Refs []string `json:"refs"`
	// Count is how many underlying references were merged into this edge.
	Count int `json:"count"`
}

// Subgraph is a scoped view of the dependency graph.
type Subgraph struct {
	Focus string         `json:"focus"`
	Nodes []SubgraphNode `json:"nodes"`
	Edges []SubgraphEdge `json:"edges"`
	// Granularity is the level the view was actually drawn at, which under GranularityAuto is
	// a consequence of how big the scope turned out to be.
	Granularity string `json:"granularity"`
	// Reach is the furthest hop the walk got to before it ran out of graph or budget.
	Reach     int  `json:"reach"`
	Truncated bool `json:"truncated"`
}

// SubgraphOptions scopes a view.
type SubgraphOptions struct {
	// Focus may be empty, which asks for an overview of the whole corpus.
	Focus string
	// Hops of zero or less means no hop limit.
	Hops int
	// MaxNodes caps what is returned; zero uses DefaultSubgraphNodes.
	MaxNodes int
	// Direction defaults to DirectionDependents.
	Direction string
	// Granularity defaults to GranularityWorkflow.
	Granularity string
	// Expand keeps steps and jobs instead of folding them into their container.
	Expand bool
}

func (o SubgraphOptions) maxNodes() int {
	if o.MaxNodes <= 0 {
		return DefaultSubgraphNodes
	}
	return o.MaxNodes
}

func (o SubgraphOptions) hops() int {
	if o.Hops <= 0 {
		return unboundedHops
	}
	return o.Hops
}

// Subgraph returns the neighborhood around a focus node, or an overview when none is given.
//
// The walk is breadth-first and bounded by node count, so the cost is a function of the scope
// rather than of the corpus. Nothing loads the whole graph.
func (s *Store) Subgraph(opts SubgraphOptions) (Subgraph, error) {
	if opts.Focus == "" {
		return s.overview(opts)
	}
	if opts.Granularity == "" {
		opts.Granularity = GranularityWorkflow
	}
	return s.walk(opts)
}

func (s *Store) walk(opts SubgraphOptions) (Subgraph, error) {
	out := Subgraph{Focus: opts.Focus, Granularity: opts.Granularity}

	var focusID int64
	err := s.db.QueryRow(`SELECT row_id FROM abom_node_live WHERE node_key = ?`, opts.Focus).Scan(&focusID)
	if err == sql.ErrNoRows {
		out.Nodes, out.Edges = []SubgraphNode{}, []SubgraphEdge{}
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("resolving focus %q: %w", opts.Focus, err)
	}

	v := &subgraphWalk{
		store:    s,
		opts:     opts,
		display:  map[int64]int64{},
		hops:     map[int64]int{focusID: 0},
		edges:    map[[2]int64]*SubgraphEdge{},
		budgeted: map[int64]bool{focusID: true},
	}

	frontier := []int64{focusID}
	for hop := 1; hop <= opts.hops() && len(frontier) > 0; hop++ {
		next, err := v.step(frontier, hop)
		if err != nil {
			return out, err
		}
		if len(next) > 0 {
			v.reach = hop
		}
		frontier = next
	}
	return v.result(focusID)
}

// subgraphWalk carries the state of one bounded traversal.
type subgraphWalk struct {
	store *Store
	opts  SubgraphOptions
	// display maps a node to the node that stands for it once collapsing is applied.
	display  map[int64]int64
	hops     map[int64]int
	edges    map[[2]int64]*SubgraphEdge
	budgeted map[int64]bool
	reach    int
	full     bool
}

func (v *subgraphWalk) step(frontier []int64, hop int) ([]int64, error) {
	var next []int64
	for _, node := range frontier {
		rows, err := v.neighbors(node)
		if err != nil {
			return nil, err
		}
		for _, n := range rows {
			shown, err := v.displayOf(n.other)
			if err != nil {
				return nil, err
			}
			if shown == node {
				// A step and its own workflow are the same thing once collapsed.
				continue
			}

			if _, seen := v.hops[shown]; !seen {
				if len(v.budgeted) >= v.opts.maxNodes() {
					v.full = true
					continue
				}
				v.hops[shown] = hop
				v.budgeted[shown] = true
				next = append(next, shown)
			}
			v.record(shown, node, n)
		}
	}
	return next, nil
}

// neighbor is one edge reached during the walk.
type neighbor struct {
	other int64
	kind  string
	ref   string
	// outgoing records which way the edge actually runs, so a two-way walk does not invent
	// arrows that claim a dependency points the wrong way.
	outgoing bool
}

func (v *subgraphWalk) neighbors(node int64) ([]neighbor, error) {
	// A displayed node stands for its members, so their edges are its edges.
	members := `SELECT ? UNION SELECT row_id FROM abom_node_live WHERE container_node_row_id = ?`
	if v.opts.Granularity == GranularityRepository {
		members = `SELECT ? UNION SELECT row_id FROM abom_node_live
		           WHERE repository_row_id = (
		                 SELECT repository_row_id FROM abom_node_live WHERE row_id = ?)`
	}

	var parts []string
	var args []any
	add := func(side, other string, outgoing int) {
		parts = append(parts, fmt.Sprintf(`
			SELECT e.%s, e.edge_kind, COALESCE(e.ref_value, ''), %d
			FROM abom_edge_regular e
			WHERE e.%s IN (%s)`, other, outgoing, side, members))
		args = append(args, node, node)
	}
	switch v.opts.Direction {
	case DirectionDependencies:
		add("src_node_row_id", "dst_node_row_id", 1)
	case DirectionBoth:
		add("dst_node_row_id", "src_node_row_id", 0)
		add("src_node_row_id", "dst_node_row_id", 1)
	default:
		add("dst_node_row_id", "src_node_row_id", 0)
	}

	rows, err := v.store.db.Query(strings.Join(parts, " UNION ALL "), args...)
	if err != nil {
		return nil, fmt.Errorf("reading neighbors of %d: %w", node, err)
	}
	defer rows.Close()

	var out []neighbor
	for rows.Next() {
		var n neighbor
		if err := rows.Scan(&n.other, &n.kind, &n.ref, &n.outgoing); err != nil {
			return nil, fmt.Errorf("scanning neighbor: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// record merges an edge, keeping every observed reference.
func (v *subgraphWalk) record(shown, node int64, n neighbor) {
	src, dst := shown, node
	if n.outgoing {
		src, dst = node, shown
	}

	key := [2]int64{src, dst}
	e, ok := v.edges[key]
	if !ok {
		e = &SubgraphEdge{Kind: n.kind}
		v.edges[key] = e
	}
	e.Count++
	if n.ref != "" {
		for _, existing := range e.Refs {
			if existing == n.ref {
				return
			}
		}
		e.Refs = append(e.Refs, n.ref)
	}
}

// displayOf walks up the container chain until it reaches something a reader recognizes.
func (v *subgraphWalk) displayOf(id int64) (int64, error) {
	if cached, ok := v.display[id]; ok {
		return cached, nil
	}

	shown, err := v.resolveDisplay(id)
	if err != nil {
		return 0, err
	}
	v.display[id] = shown
	return shown, nil
}

func (v *subgraphWalk) resolveDisplay(id int64) (int64, error) {
	if v.opts.Granularity == GranularityRepository {
		// Anything a repository owns becomes the repository. Targets that belong to no
		// repository, such as a published action, are already the thing being depended on.
		var repoNode sql.NullInt64
		err := v.store.db.QueryRow(`
			SELECT owner.row_id
			FROM abom_node_live n
			JOIN repository r ON r.row_id = n.repository_row_id
			JOIN abom_node_live owner ON owner.node_kind = 'repo'
			                    AND owner.repository_row_id = r.row_id
			WHERE n.row_id = ?`, id).Scan(&repoNode)
		if err != nil && err != sql.ErrNoRows {
			return 0, fmt.Errorf("resolving repository of %d: %w", id, err)
		}
		if repoNode.Valid {
			return repoNode.Int64, nil
		}
		return id, nil
	}
	if v.opts.Expand {
		return id, nil
	}

	current := id
	for range 8 { // A container chain is workflow-deep; the bound only guards against cycles.
		var kind string
		var container sql.NullInt64
		err := v.store.db.QueryRow(
			`SELECT node_kind, container_node_row_id FROM abom_node_live WHERE row_id = ?`, current).
			Scan(&kind, &container)
		if err != nil {
			return 0, fmt.Errorf("resolving container of %d: %w", current, err)
		}
		if !collapsedKinds[kind] || !container.Valid {
			break
		}
		current = container.Int64
	}
	return current, nil
}

// result materializes the walked ids into the returned view.
func (v *subgraphWalk) result(focusID int64) (Subgraph, error) {
	// Empty, not nil: a nil slice marshals to null, and a client reading a collection as null
	// has to special-case what is simply an empty result.
	out := Subgraph{
		Focus:       v.opts.Focus,
		Granularity: v.opts.Granularity,
		Reach:       v.reach,
		Truncated:   v.full,
		Nodes:       []SubgraphNode{},
		Edges:       []SubgraphEdge{},
	}

	for id, hops := range v.hops {
		n, err := v.node(id)
		if err != nil {
			return out, err
		}
		n.Hops = hops
		out.Nodes = append(out.Nodes, n)
	}
	sort.Slice(out.Nodes, func(i, j int) bool {
		if out.Nodes[i].Hops != out.Nodes[j].Hops {
			return out.Nodes[i].Hops < out.Nodes[j].Hops
		}
		return out.Nodes[i].Key < out.Nodes[j].Key
	})

	keyOf := map[int64]string{}
	for id := range v.hops {
		n, err := v.node(id)
		if err != nil {
			return out, err
		}
		keyOf[id] = n.Key
	}
	for pair, e := range v.edges {
		src, srcOK := keyOf[pair[0]]
		dst, dstOK := keyOf[pair[1]]
		if !srcOK || !dstOK {
			// One end fell outside the budget, so the edge would dangle.
			continue
		}
		e.Source, e.Target = src, dst
		sort.Strings(e.Refs)
		out.Edges = append(out.Edges, *e)
	}
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].Source != out.Edges[j].Source {
			return out.Edges[i].Source < out.Edges[j].Source
		}
		return out.Edges[i].Target < out.Edges[j].Target
	})
	return out, nil
}

func (v *subgraphWalk) node(id int64) (SubgraphNode, error) {
	var n SubgraphNode
	var repo, origin, path sql.NullString
	err := v.store.db.QueryRow(`
		SELECT n.node_key, n.node_kind, r.repository_full_name, r.origin, n.path, n.is_external,
		       COALESCE(b.dependent_repos, 0),
		       (SELECT COUNT(*) FROM abom_finding_node fn WHERE fn.node_row_id = n.row_id)
		FROM abom_node_live n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		WHERE n.row_id = ?`, id).
		Scan(&n.Key, &n.Kind, &repo, &origin, &path, &n.IsExternal,
			&n.DependentRepos, &n.Findings)
	if err != nil {
		return n, fmt.Errorf("reading node %d: %w", id, err)
	}
	n.Repository, n.Origin, n.Path = repo.String, origin.String, path.String
	return n, nil
}
