package store

import (
	"fmt"
	"sort"
)

// refNodeKey is how a version appears in the graph. An empty reference keeps the bare name: a
// `uses:` that names no reference runs the default branch, and inventing one would put a version
// on screen that the workflow never wrote.
func refNodeKey(nodeKey, ref string) string {
	if ref == "" {
		return nodeKey
	}
	return nodeKey + "@" + ref
}

// refState is one queue row, which is one version of one dependency.
type refState struct {
	status    string
	analyzed  bool
	depth     int
	score     float64
	dependent int
}

// splitNodesByRef expands each dependency into one node per reference actually in use.
//
// The queue analyzes `actions/checkout@v4` and `@v3` independently, and their findings need not
// agree, so drawing one node for both reports a single state for two separate analyses. The split
// is deliberately a *view*: `abom_node` stays a component identity, because findings, exposure and
// blast radius all join it and a version identity would change what those rows mean.
//
// Reach is recomputed per reference rather than inherited from the name. The name's blast radius
// counts every repository using any version, so copying it onto each version would report the same
// dependents several times over.
func (s *Store) splitNodesByRef(nodes []GraphNode, edges []GraphEdge) ([]GraphNode, []GraphEdge, error) {
	states, err := s.refStates()
	if err != nil {
		return nil, nil, err
	}
	if len(states) == 0 {
		return nodes, edges, nil
	}

	// Only what the queue fetches per reference. A reusable workflow inside a repository under
	// review is analyzed with its repository, not per reference, and splitting it would strand
	// the edges that run out of it — which is exactly how the exposure scope caught this.
	queued := map[string]bool{}
	analyzedRef := map[string]string{}
	for key, st := range states {
		queued[key[0]] = true
		if st.analyzed {
			analyzedRef[key[0]] = key[1]
		}
	}

	splittable := map[string]bool{}
	for _, n := range nodes {
		if queued[n.Key] {
			splittable[n.Key] = true
		}
	}
	// Edges run out of a dependency only because we read one checkout of it. Without a record of
	// which version that was, there is no version to hang them on, and splitting the node would
	// have to invent one. Leave it whole and say nothing rather than guess.
	for _, e := range edges {
		if splittable[e.Source] && analyzedRef[e.Source] == "" {
			if _, ok := analyzedRef[e.Source]; !ok {
				delete(splittable, e.Source)
			}
		}
	}
	if len(splittable) == 0 {
		return nodes, edges, nil
	}

	// Which references each dependency is actually used at, from the edges that name them.
	refsOf := map[string]map[string]bool{}
	for _, e := range edges {
		if !splittable[e.Target] {
			continue
		}
		seen := refsOf[e.Target]
		if seen == nil {
			seen = map[string]bool{}
			refsOf[e.Target] = seen
		}
		if len(e.Refs) == 0 {
			seen[""] = true
			continue
		}
		for _, r := range e.Refs {
			seen[r] = true
		}
	}

	// A version we analyzed is a version that exists, whether or not anything in the corpus is
	// seen using it. Its steps are already edges out of it, so omitting the node it ran as would
	// strand them: 700 dangling sources, all of them composite actions we had in fact read.
	for _, e := range edges {
		ref, ok := analyzedRef[e.Source]
		if !ok || !splittable[e.Source] {
			continue
		}
		if refsOf[e.Source] == nil {
			refsOf[e.Source] = map[string]bool{}
		}
		refsOf[e.Source][ref] = true
	}

	out := make([]GraphNode, 0, len(nodes))
	for _, n := range nodes {
		refs := refsOf[n.Key]
		if !splittable[n.Key] || len(refs) == 0 {
			out = append(out, n)
			continue
		}
		for _, ref := range sortedKeys(refs) {
			v := n
			v.Key = refNodeKey(n.Key, ref)
			v.Ref = ref
			// Every version starts from nothing known and is filled in by its own queue row.
			// A version nobody queued is unanalyzed, not inherited from a sibling that was.
			v.Analyzed, v.Discovery, v.Depth, v.QueueScore = false, "", 0, 0
			v.RefsInUse, v.RefsAnalyzed = 1, 0
			v.DependentRepos = 0
			if st, ok := states[[2]string{n.Key, ref}]; ok {
				v.Analyzed, v.Discovery = st.analyzed, st.status
				v.Depth, v.QueueScore = st.depth, st.score
				v.DependentRepos = st.dependent
				if st.analyzed {
					v.RefsAnalyzed = 1
				}
			}
			// Findings came from the tree that was analyzed, so a version nothing looked at
			// reports none rather than borrowing its sibling's. The rule list stays an empty
			// slice: the interface reads it as a set, and null is not one.
			if !v.Analyzed {
				v.Findings, v.Rules = 0, []string{}
			}
			out = append(out, v)
		}
	}

	return out, retargetEdges(edges, splittable, refsOf, analyzedRef), nil
}

// retargetEdges points each use at the version it names, splitting an edge that carries several.
//
// Edges *out* of a split dependency move to the version they were produced from: a composite
// action's own steps were read from one checkout, and leaving them on the bare name would strand
// them beside a node that no longer exists.
func retargetEdges(
	edges []GraphEdge, splittable map[string]bool, refsOf map[string]map[string]bool,
	analyzedRef map[string]string,
) []GraphEdge {
	out := make([]GraphEdge, 0, len(edges))
	for _, e := range edges {
		if splittable[e.Source] && len(refsOf[e.Source]) > 0 {
			e.Source = refNodeKey(e.Source, analyzedRef[e.Source])
		}
		if !splittable[e.Target] || len(refsOf[e.Target]) == 0 {
			out = append(out, e)
			continue
		}
		refs := e.Refs
		if len(refs) == 0 {
			refs = []string{""}
		}
		for _, ref := range refs {
			split := e
			split.Target = refNodeKey(e.Target, ref)
			split.Refs = []string{ref}
			if ref == "" {
				split.Refs = []string{}
			}
			out = append(out, split)
		}
	}
	return out
}

// refStates reads every queue row and the reach of the version it names. Reach is counted from
// the edges rather than from blast radius, which is keyed by name and would attribute the whole
// dependency's dependents to each of its versions.
func (s *Store) refStates() (map[[2]string]refState, error) {
	rows, err := s.db.Query(`
		SELECT f.node_key, f.ref, f.status, f.status = 'done',
		       f.depth, f.score,
		       (SELECT COUNT(DISTINCT e.repository_row_id)
		          FROM abom_edge e
		          JOIN abom_node n ON n.row_id = e.dst_node_row_id
		         WHERE n.node_key = f.node_key AND COALESCE(e.ref_value, '') = f.ref)
		FROM abom_frontier f`)
	if err != nil {
		return nil, fmt.Errorf("reading reference states: %w", err)
	}
	defer rows.Close()

	out := map[[2]string]refState{}
	for rows.Next() {
		var key, ref string
		var st refState
		if err := rows.Scan(&key, &ref, &st.status, &st.analyzed, &st.depth, &st.score,
			&st.dependent); err != nil {
			return nil, fmt.Errorf("scanning reference state: %w", err)
		}
		// The queue's states are split the way a reader reads them, as elsewhere.
		if st.status == "pending" {
			st.status = "queued"
		}
		out[[2]string{key, ref}] = st
	}
	return out, rows.Err()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
