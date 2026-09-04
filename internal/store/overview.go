package store

import (
	"fmt"
	"sort"
	"strings"
)

// overview is the whole corpus rolled up to repositories.
//
// It exists so the explorer can open on something. Requiring a focus first asks the reader to
// name the answer before they have seen the question. Workflow-level detail across the corpus is
// neither inside the node budget nor legible, so an unfocused view is always a roll-up.
func (s *Store) overview(opts SubgraphOptions) (Subgraph, error) {
	out := Subgraph{
		Granularity: GranularityRepository,
		Nodes:       []SubgraphNode{},
		Edges:       []SubgraphEdge{},
	}

	rows, err := s.db.Query(`
		SELECT COALESCE(srep.node_key, sn.node_key), COALESCE(drep.node_key, dn.node_key),
		       e.edge_kind, COALESCE(e.ref_value, '')
		FROM abom_edge e
		JOIN abom_node sn ON sn.row_id = e.src_node_row_id
		JOIN abom_node dn ON dn.row_id = e.dst_node_row_id
		LEFT JOIN abom_node srep ON srep.node_kind = 'repo'
		                        AND srep.repository_row_id = sn.repository_row_id
		LEFT JOIN abom_node drep ON drep.node_kind = 'repo'
		                        AND drep.repository_row_id = dn.repository_row_id`)
	if err != nil {
		return out, fmt.Errorf("reading the overview: %w", err)
	}
	defer rows.Close()

	merged := map[[2]string]*SubgraphEdge{}
	degree := map[string]int{}
	for rows.Next() {
		var src, dst, kind, ref string
		if err := rows.Scan(&src, &dst, &kind, &ref); err != nil {
			return out, fmt.Errorf("scanning overview edge: %w", err)
		}
		if src == dst {
			// Both ends folded into the same repository, so the edge is internal to it.
			continue
		}
		key := [2]string{src, dst}
		e, ok := merged[key]
		if !ok {
			e = &SubgraphEdge{Source: src, Target: dst, Kind: kind}
			merged[key] = e
			degree[src]++
			degree[dst]++
		}
		e.Count++
		e.addRef(ref)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("reading overview edges: %w", err)
	}

	kept := busiest(degree, opts.maxNodes())
	out.Truncated = len(kept) < len(degree)

	for _, e := range merged {
		if !kept[e.Source] || !kept[e.Target] {
			continue
		}
		sort.Strings(e.Refs)
		out.Edges = append(out.Edges, *e)
	}
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].Source != out.Edges[j].Source {
			return out.Edges[i].Source < out.Edges[j].Source
		}
		return out.Edges[i].Target < out.Edges[j].Target
	})

	keys := make([]string, 0, len(kept))
	for k := range kept {
		keys = append(keys, k)
	}
	nodes, err := s.nodesByKey(keys)
	if err != nil {
		return out, err
	}
	out.Nodes = nodes
	return out, nil
}

func (e *SubgraphEdge) addRef(ref string) {
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

// busiest keeps the most connected nodes when the budget cannot hold all of them, so what
// survives truncation is what the corpus turns on rather than whatever sorted first.
func busiest(degree map[string]int, max int) map[string]bool {
	keys := make([]string, 0, len(degree))
	for k := range degree {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if degree[keys[i]] != degree[keys[j]] {
			return degree[keys[i]] > degree[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > max {
		keys = keys[:max]
	}
	kept := make(map[string]bool, len(keys))
	for _, k := range keys {
		kept[k] = true
	}
	return kept
}

func (s *Store) nodesByKey(keys []string) ([]SubgraphNode, error) {
	out := []SubgraphNode{}
	if len(keys) == 0 {
		return out, nil
	}

	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	rows, err := s.db.Query(`
		SELECT n.node_key, n.node_kind, COALESCE(r.repository_full_name, ''),
		       COALESCE(r.origin, ''), COALESCE(n.path, ''), n.is_external,
		       COALESCE(b.dependent_repos, 0),
		       (SELECT COUNT(*) FROM abom_finding_node fn WHERE fn.node_row_id = n.row_id)
		FROM abom_node n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		WHERE n.node_key IN (?`+strings.Repeat(",?", len(keys)-1)+`)`, args...)
	if err != nil {
		return out, fmt.Errorf("reading overview nodes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var n SubgraphNode
		if err := rows.Scan(&n.Key, &n.Kind, &n.Repository, &n.Origin, &n.Path, &n.IsExternal,
			&n.DependentRepos, &n.Findings); err != nil {
			return out, fmt.Errorf("scanning overview node: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("reading overview nodes: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
