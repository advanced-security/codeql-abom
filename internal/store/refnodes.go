package store

import (
	"fmt"
	"sort"
	"strings"
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

// refBaseKey is the inverse: the undivided node a version was drawn from. Exact rather than a
// search for the last `@`, because a node key may hold one of its own.
func refBaseKey(nodeKey, ref string) string {
	if ref == "" {
		return nodeKey
	}
	return strings.TrimSuffix(nodeKey, "@"+ref)
}

// refState is one queue row, which is one version of one dependency.
type refState struct {
	status    string
	analyzed  bool
	depth     int
	score     float64
	dependent int
	// analyzedAt decides which version an edge out of this dependency belongs to. Ingestion
	// upserts, so the newest analysis is the one whose results the graph currently holds.
	analyzedAt string
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

	queued, analyzedRefs := indexRefStates(states)
	splittable := splittableNodes(nodes, edges, queued, analyzedRefs)
	if len(splittable) == 0 {
		return nodes, edges, nil
	}
	refsOf := refsInUse(edges, splittable, analyzedRefs)

	return expandNodesByRef(nodes, splittable, refsOf, states),
		retargetEdges(edges, splittable, refsOf, sourceRefs(analyzedRefs, states)), nil
}

// indexRefStates groups the queue rows by dependency: everything queued, and the analyzed
// references of each.
//
// Every analyzed reference is kept, not one of them: `states` is keyed by pair, so a single-valued
// map here gave a dependency analyzed at nine references a node for whichever one Go's randomized
// iteration visited last, and a different one on every read.
func indexRefStates(states map[[2]string]refState) (map[string]bool, map[string]map[string]bool) {
	queued := map[string]bool{}
	analyzedRefs := map[string]map[string]bool{}
	for key, st := range states {
		queued[key[0]] = true
		if st.analyzed {
			if analyzedRefs[key[0]] == nil {
				analyzedRefs[key[0]] = map[string]bool{}
			}
			analyzedRefs[key[0]][key[1]] = true
		}
	}
	return queued, analyzedRefs
}

// splittableNodes picks the dependencies that may be drawn once per reference.
//
// Only what the queue fetches per reference. A reusable workflow inside a repository under review
// is analyzed with its repository, not per reference, and splitting it would strand the edges that
// run out of it — which is exactly how the exposure scope caught this.
func splittableNodes(
	nodes []GraphNode, edges []GraphEdge,
	queued map[string]bool, analyzedRefs map[string]map[string]bool,
) map[string]bool {
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
		if splittable[e.Source] && len(analyzedRefs[e.Source]) == 0 {
			delete(splittable, e.Source)
		}
	}
	return splittable
}

// refsInUse collects the references each splittable dependency has to be drawn at.
func refsInUse(
	edges []GraphEdge, splittable map[string]bool, analyzedRefs map[string]map[string]bool,
) map[string]map[string]bool {
	refsOf := map[string]map[string]bool{}
	add := func(key, ref string) {
		if refsOf[key] == nil {
			refsOf[key] = map[string]bool{}
		}
		refsOf[key][ref] = true
	}
	// Which references each dependency is actually used at, from the edges that name them.
	for _, e := range edges {
		if !splittable[e.Target] {
			continue
		}
		if len(e.Refs) == 0 {
			add(e.Target, "")
			continue
		}
		for _, r := range e.Refs {
			add(e.Target, r)
		}
	}
	// A version we analyzed is a version that exists, whether or not anything in the corpus is
	// seen using it. Its steps are already edges out of it, so omitting the node it ran as would
	// strand them: 700 dangling sources, all of them composite actions we had in fact read.
	for _, e := range edges {
		if !splittable[e.Source] {
			continue
		}
		for ref := range analyzedRefs[e.Source] {
			add(e.Source, ref)
		}
	}
	return refsOf
}

// expandNodesByRef draws one node per reference in use, each filled in from its own queue row.
func expandNodesByRef(
	nodes []GraphNode, splittable map[string]bool,
	refsOf map[string]map[string]bool, states map[[2]string]refState,
) []GraphNode {
	out := make([]GraphNode, 0, len(nodes))
	for _, n := range nodes {
		refs := refsOf[n.Key]
		if !splittable[n.Key] || len(refs) == 0 {
			out = append(out, n)
			continue
		}
		for _, ref := range sortedKeys(refs) {
			out = append(out, nodeAtRef(n, ref, states))
		}
	}
	return out
}

// nodeAtRef is one dependency as one version of itself.
func nodeAtRef(n GraphNode, ref string, states map[[2]string]refState) GraphNode {
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
	return v
}

// sourceRefs picks the one version an edge out of a dependency is attributed to.
//
// The bare node is gone once a dependency is split, so every edge leaving it must name a version
// or it dangles. Where several versions were analyzed the newest one wins: ingestion upserts, so
// its results are the ones the graph currently holds. Ties break on the reference name, because
// two versions analyzed in the same second must still resolve the same way on every read.
func sourceRefs(
	analyzedRefs map[string]map[string]bool, states map[[2]string]refState,
) map[string]string {
	out := map[string]string{}
	for key, refs := range analyzedRefs {
		best, bestAt := "", ""
		first := true
		for _, ref := range sortedKeys(refs) {
			at := states[[2]string{key, ref}].analyzedAt
			if first || at > bestAt || (at == bestAt && ref > best) {
				best, bestAt, first = ref, at, false
			}
		}
		out[key] = best
	}
	return out
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
			// Only the relationships that named this version. Copying the merged edge wholesale
			// would tell a reader selecting v3 that they had selected the line that pinned v4.
			split.parts = partsAtRef(e.parts, ref)
			split.summarize()
			out = append(out, split)
		}
	}
	return out
}

// partsAtRef keeps the relationships that named one version.
func partsAtRef(parts []edgePart, ref string) []edgePart {
	out := make([]edgePart, 0, len(parts))
	for _, p := range parts {
		if p.ref == ref {
			out = append(out, p)
		}
	}
	return out
}

// usersPerRef counts the repositories naming each (node_key, ref) exactly once for the whole
// query, instead of once per row that asks.
//
// Shared rather than written twice: the queue view and the graph both answer "who runs this exact
// version" from the same edges, and `ownedFullName` exists because the tier, the queue and the
// node users had already drifted into three answers to one question.
// `TestTheQueueAndTheGraphAgreeOnHowManyRepositoriesNameAVersion` is the guard.
//
// Matched on the edge's ref rather than the node, because one action at two references is two
// different things to everyone downstream.
const usersPerRef = `SELECT n.node_key AS nk, COALESCE(e.ref_value, '') AS rf,
	       COUNT(DISTINCT e.repository_row_id) AS cnt
	  FROM abom_edge_live e
	  JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	 GROUP BY n.node_key, COALESCE(e.ref_value, '')`

// refStatesQuery is separate from the read so its plan can be asserted: whether this walks the
// edge table once per queue row is a structural property, not something a stopwatch decides.
//
// Measured 2026-09-11: as a correlated subquery this was **30.07% of `/api/graph/full`**, more
// than reading every edge in the corpus.
const refStatesQuery = `
	WITH users AS (` + usersPerRef + `)
	SELECT f.node_key, f.ref, f.status, f.status = 'done',
	       f.depth, f.score,
	       COALESCE(u.cnt, 0),
	       COALESCE(f.analyzed_at, '')
	FROM abom_frontier f
	LEFT JOIN users u ON u.nk = f.node_key AND u.rf = f.ref`

// refStates reads every queue row and the reach of the version it names. Reach is counted from
// the edges rather than from blast radius, which is keyed by name and would attribute the whole
// dependency's dependents to each of its versions.
func (s *Store) refStates() (map[[2]string]refState, error) {
	rows, err := s.db.Query(refStatesQuery)
	if err != nil {
		return nil, fmt.Errorf("reading reference states: %w", err)
	}
	defer rows.Close()

	out := map[[2]string]refState{}
	for rows.Next() {
		var key, ref string
		var st refState
		if err := rows.Scan(&key, &ref, &st.status, &st.analyzed, &st.depth, &st.score,
			&st.dependent, &st.analyzedAt); err != nil {
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
