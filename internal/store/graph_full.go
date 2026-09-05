package store

import (
	"fmt"
	"sort"
)

// GraphNode is one node in the whole-corpus view.
//
// It carries everything the interface filters on, because filtering a loaded graph in the
// browser is the point: a filter that needed another round trip would stop being instant.
type GraphNode struct {
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	Repository string `json:"repository"`
	// Origin distinguishes repositories under review from those fetched as dependencies.
	Origin    string `json:"origin"`
	Path      string `json:"path"`
	Container string `json:"container,omitempty"`
	// Owner is the account a dependency lives under, which is the coarsest grouping available.
	Owner      string `json:"owner,omitempty"`
	IsExternal bool   `json:"isExternal"`
	// DependentRepos is blast radius, and is what sizes a node.
	DependentRepos int `json:"dependentRepos"`
	// ReachKnown is false for a node added since reach was last recomputed. Its DependentRepos
	// is then absent rather than zero, and drawing it as zero-reach would assert a measurement
	// nobody made.
	ReachKnown bool `json:"reachKnown"`
	Findings   int  `json:"findings"`
	// Rules are the CodeQL rule ids that produced this node's findings. Every verdict in the
	// interface is filtered by these, never by a property this package inferred.
	Rules []string `json:"rules"`
	// Advisories counts published advisories naming this node's repository. It is a count of
	// statements, not a judgement that any observed reference is affected.
	Advisories int `json:"advisories"`
	// Analyzed reports whether the dependency itself was expanded, which is what decides
	// whether anything downstream of it is known at all.
	Analyzed bool `json:"analyzed"`
	// Discovery is the node's place in the queue: queued, retrying, running, done, unavailable,
	// or empty for the majority of nodes, which are not dependencies and were never queued.
	// Empty is not "unknown" and must not be drawn as though it were.
	Discovery string `json:"discovery,omitempty"`
	// Depth is hops from the seed set for a queued dependency, and zero for everything else.
	Depth int `json:"depth,omitempty"`
	// QueueScore is commonality per unit cost, the order discovery works in.
	QueueScore float64 `json:"queueScore,omitempty"`
}

// GraphEdge is a dependency relationship, merged across the references that expressed it.
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	// Refs are the references observed for this relationship, verbatim and unjudged.
	Refs  []string `json:"refs"`
	Count int      `json:"count"`
}

// Graph is the whole corpus.
type Graph struct {
	Nodes         []GraphNode `json:"nodes"`
	Edges         []GraphEdge `json:"edges"`
	IncludesSteps bool        `json:"includesSteps"`
}

// FullGraphOptions selects what the whole-corpus view contains.
type FullGraphOptions struct {
	// IncludeSteps keeps every individual `uses:` line rather than folding it into the
	// workflow or composite action that holds it. Roughly 23k nodes against 3.6k.
	IncludeSteps bool
}

// display folds steps and jobs into the container that holds them.
//
// One level is enough: every step and job has a container that is itself neither, so the
// collapsed node count is exactly the count of everything else.
const collapsedDisplay = `disp AS (
	SELECT n.row_id, COALESCE(c.row_id, n.row_id) AS shown
	FROM abom_node n
	LEFT JOIN abom_node c ON c.row_id = n.container_node_row_id)`

const identityDisplay = `disp AS (SELECT row_id, row_id AS shown FROM abom_node)`

// frontierState is a node's place in the discovery queue, split the way a reader reads it.
// Most nodes are not dependencies and have no row; that is an empty string, not a state.
const frontierState = `(SELECT CASE
				 WHEN f.status = 'pending' AND f.attempts > 0 THEN 'retrying'
				 WHEN f.status = 'pending' THEN 'queued'
				 ELSE f.status
			   END FROM abom_frontier f WHERE f.node_key = n.node_key)`

// Only nodes that appear as an edge target get a row, so a missing row usually means nothing
// references this node and its reach is genuinely zero. It means "not measured" only for a node
// that arrived after the last rebuild, which is what the watermark separates.
const reachKnown = `(b.node_row_id IS NOT NULL OR n.row_id <= COALESCE((
	SELECT node_watermark FROM abom_graph_generation
	WHERE reach_rebuilt = 1 ORDER BY generation DESC LIMIT 1), 0))`

// FullGraph has no node budget. The interface loads it once and every filter afterwards runs
// against it in the browser, so the cost is paid on arrival rather than on every interaction.
func (s *Store) FullGraph(opts FullGraphOptions) (Graph, error) {
	out := Graph{Nodes: []GraphNode{}, Edges: []GraphEdge{}, IncludesSteps: opts.IncludeSteps}

	display := collapsedDisplay
	if opts.IncludeSteps {
		display = identityDisplay
	}

	nodes, index, err := s.graphNodes(display, 0)
	if err != nil {
		return out, err
	}
	if err := s.attachRules(display, index); err != nil {
		return out, err
	}
	edges, err := s.graphEdges(display, 0)
	if err != nil {
		return out, err
	}

	nodes, edges = attachOwners(nodes, edges)

	out.Nodes, out.Edges = nodes, edges
	return out, nil
}

// graphNodes reads the displayed nodes. A non-zero since restricts them to those a node added
// after that row id displays as, which is how a delta is derived.
func (s *Store) graphNodes(display string, since int64) ([]GraphNode, map[string]*GraphNode, error) {
	rows, err := s.db.Query(`WITH `+display+`
		SELECT n.node_key, n.node_kind, COALESCE(r.repository_full_name, ''),
		       COALESCE(r.origin, ''), COALESCE(n.path, ''), COALESCE(c.node_key, ''),
		       COALESCE(n.owner, ''),
		       n.is_external, COALESCE(b.dependent_repos, 0), `+reachKnown+`,
		       (SELECT COUNT(DISTINCT a.ghsa_id)
		          FROM abom_advisory a
		          JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		         WHERE a.withdrawn_at IS NULL
		           AND n.owner IS NOT NULL AND n.repo IS NOT NULL
		           AND p.package_name = n.owner || '/' || n.repo),
		       EXISTS (SELECT 1 FROM abom_frontier f
		                WHERE f.node_key = n.node_key AND f.status = 'done'),
		       COALESCE(`+frontierState+`, ''),
		       COALESCE((SELECT f.depth FROM abom_frontier f WHERE f.node_key = n.node_key), 0),
		       COALESCE((SELECT f.score FROM abom_frontier f WHERE f.node_key = n.node_key), 0)
		FROM abom_node n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		LEFT JOIN abom_node c ON c.row_id = n.container_node_row_id
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		WHERE n.row_id IN (SELECT shown FROM disp WHERE row_id > ?)`, since)
	if err != nil {
		return nil, nil, fmt.Errorf("reading graph nodes: %w", err)
	}
	defer rows.Close()

	nodes := []GraphNode{}
	for rows.Next() {
		var n GraphNode
		if err := rows.Scan(&n.Key, &n.Kind, &n.Repository, &n.Origin, &n.Path, &n.Container,
			&n.Owner, &n.IsExternal, &n.DependentRepos, &n.ReachKnown, &n.Advisories,
			&n.Analyzed, &n.Discovery, &n.Depth, &n.QueueScore); err != nil {
			return nil, nil, fmt.Errorf("scanning graph node: %w", err)
		}
		n.Rules = []string{}
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading graph nodes: %w", err)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Key < nodes[j].Key })

	index := make(map[string]*GraphNode, len(nodes))
	for i := range nodes {
		index[nodes[i].Key] = &nodes[i]
	}
	return nodes, index, nil
}

// attachRules carries the rule ids that produced each node's findings.
//
// A finding recorded against a step belongs to the workflow once the step is folded away, or
// collapsing would silently drop the evidence along with the node.
func (s *Store) attachRules(display string, index map[string]*GraphNode) error {
	rows, err := s.db.Query(`WITH ` + display + `
		SELECT shown.node_key, f.rule_id, COUNT(*)
		FROM abom_finding_node fn
		JOIN disp d ON d.row_id = fn.node_row_id
		JOIN abom_node shown ON shown.row_id = d.shown
		JOIN abom_finding f ON f.row_id = fn.finding_row_id
		GROUP BY shown.node_key, f.rule_id`)
	if err != nil {
		return fmt.Errorf("reading findings by rule: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, rule string
		var count int
		if err := rows.Scan(&key, &rule, &count); err != nil {
			return fmt.Errorf("scanning finding rule: %w", err)
		}
		n, ok := index[key]
		if !ok {
			continue
		}
		n.Findings += count
		n.Rules = append(n.Rules, rule)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading findings by rule: %w", err)
	}
	for _, n := range index {
		sort.Strings(n.Rules)
	}
	return nil
}

func (s *Store) graphEdges(display string, since int64) ([]GraphEdge, error) {
	// A repository is otherwise an island: nothing in abom_edge points at it, so without the
	// containment half of this union every repo node renders unattached and isolating one finds
	// nothing. Containment stops at what the repository directly holds, because wiring every
	// step to it as well would add tens of thousands of edges saying nothing new.
	rows, err := s.db.Query(`WITH `+display+`
		SELECT sn.node_key, dn.node_key, e.edge_kind, COALESCE(e.ref_value, '')
		FROM abom_edge e
		JOIN disp ds ON ds.row_id = e.src_node_row_id
		JOIN disp dd ON dd.row_id = e.dst_node_row_id
		JOIN abom_node sn ON sn.row_id = ds.shown
		JOIN abom_node dn ON dn.row_id = dd.shown
		WHERE ds.shown <> dd.shown AND e.row_id > ?
		UNION ALL
		SELECT owner.node_key, held.node_key, 'repo-contains', ''
		FROM abom_node owner
		JOIN abom_node held ON held.repository_row_id = owner.repository_row_id
		                   AND held.row_id <> owner.row_id
		                   AND held.container_node_row_id IS NULL
		                   AND held.node_kind <> 'repo'
		WHERE owner.node_kind = 'repo' AND held.row_id > ?`, since, since)
	if err != nil {
		return nil, fmt.Errorf("reading graph edges: %w", err)
	}
	defer rows.Close()

	merged := map[[2]string]*GraphEdge{}
	order := [][2]string{}
	for rows.Next() {
		var src, dst, kind, ref string
		if err := rows.Scan(&src, &dst, &kind, &ref); err != nil {
			return nil, fmt.Errorf("scanning graph edge: %w", err)
		}
		key := [2]string{src, dst}
		e, ok := merged[key]
		if !ok {
			e = &GraphEdge{Source: src, Target: dst, Kind: kind, Refs: []string{}}
			merged[key] = e
			order = append(order, key)
		}
		e.Count++
		e.addGraphRef(ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading graph edges: %w", err)
	}

	sort.Slice(order, func(i, j int) bool {
		if order[i][0] != order[j][0] {
			return order[i][0] < order[j][0]
		}
		return order[i][1] < order[j][1]
	})
	edges := make([]GraphEdge, 0, len(order))
	for _, key := range order {
		e := merged[key]
		sort.Strings(e.Refs)
		edges = append(edges, *e)
	}
	return edges, nil
}

func (e *GraphEdge) addGraphRef(ref string) {
	if ref == "" {
		return
	}
	for _, existing := range e.Refs {
		if existing == ref {
			return
		}
	}
	e.Refs = append(e.Refs, ref)
}
