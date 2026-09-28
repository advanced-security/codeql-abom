package store

import (
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"sort"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
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
	Owner string `json:"owner,omitempty"`
	// Grouping is the value of the configured repository property for the repository this node
	// lives in, empty unless that repository is inside the owned organization and carries it. A
	// dependency never inherits one: a placement is read from the organization, never guessed from
	// an edge, and upstream repositories are not somewhere ABOM reads properties from at all.
	Grouping   string `json:"grouping,omitempty"`
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
	// Facets are the distinct kinds of finding this node carries, each kept whole so a filter
	// asking for a rule and a severity matches one finding that is both.
	Facets []FindingFacet `json:"facets"`
	// Advisories counts published advisories naming this node's repository. It is a count of
	// statements, not a judgement that any observed reference is affected.
	Advisories int `json:"advisories"`
	// Analyzed reports whether the dependency itself was expanded, which is what decides
	// whether anything downstream of it is known at all. It is true when *any* reference has
	// results, so it says nothing on its own about how much of the dependency was looked at.
	Analyzed bool `json:"analyzed"`
	// RefsInUse and RefsAnalyzed are how much of this dependency has been looked at, because the
	// queue analyzes a reference and the graph draws a name. One analyzed reference out of
	// twenty-seven is not the same fact as twenty-seven of twenty-seven, and a single boolean
	// cannot tell them apart. Both zero means nothing has ever queued it, which is not coverage.
	RefsInUse    int `json:"refsInUse"`
	RefsAnalyzed int `json:"refsAnalyzed"`
	// Ref is the version this node stands for, empty for a dependency used without one. The
	// graph draws a node per version because the queue analyzes one per version.
	Ref string `json:"ref,omitempty"`
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
	// Boundary is what the relationship crosses: see BoundarySameRepository and its siblings.
	// Empty means the target names no repository, which is unknown rather than a fourth class.
	Boundary string `json:"boundary,omitempty"`
	// Refs are the references observed for this relationship, verbatim and unjudged.
	Refs  []string `json:"refs"`
	Count int      `json:"count"`
	// Keys names every stored relationship this line stands for, so a selection or a delta
	// removal identifies actual edges. Empty on the containment edges the interface synthesizes:
	// they are drawn from node rows, are declared nowhere, and have nothing to name.
	Keys []string `json:"keys,omitempty"`
	// RefStyle, AdvisoryRoute, FindingRoute and OwnedDepth are the derived facts, carried only
	// where every constituent agrees. Absent means either that nothing computed the fact or that
	// the constituents disagree, and Mixed is what distinguishes those two.
	RefStyle      string `json:"refStyle,omitempty"`
	AdvisoryRoute *bool  `json:"advisoryRoute,omitempty"`
	FindingRoute  *bool  `json:"findingRoute,omitempty"`
	OwnedDepth    *int   `json:"ownedDepth,omitempty"`
	// Mixed names the facts the constituents disagree about. Keeping the first row's value would
	// report one relationship's fact as the pair's, which is invisible when they agree and wrong
	// exactly when the difference matters.
	Mixed []string `json:"mixed,omitempty"`
	// Locked are the commits current locks select for the relationships behind the line: what
	// runs, beside the references as written. Empty where nothing locks them.
	Locked []string `json:"locked"`

	// parts are the rows behind the line. Unexported, so they stay out of the payload: a reader
	// acts on the keys and on shared-or-mixed facts, and these are here only so that splitting a
	// dependency into its versions can partition them.
	parts []edgePart
}

// edgePart is one stored relationship drawn as part of an edge.
type edgePart struct {
	key      string
	locked   []string
	ref      string
	kind     string
	boundary string
	refStyle string
	advisory sql.NullBool
	finding  sql.NullBool
	depth    sql.NullInt64
}

// The facts a merged edge reports, named as the payload names them.
const (
	edgeFactKind     = "kind"
	edgeFactBoundary = "boundary"
	edgeFactRefStyle = "refStyle"
	edgeFactAdvisory = "advisoryRoute"
	edgeFactFinding  = "findingRoute"
	edgeFactDepth    = "ownedDepth"
)

// IsMixed reports whether the relationships behind this edge disagree about a fact.
func (e GraphEdge) IsMixed(fact string) bool {
	for _, m := range e.Mixed {
		if m == fact {
			return true
		}
	}
	return false
}

// summarize derives the edge's identity and facts from the relationships behind it.
//
// Every fact is either shared by all of them or named in Mixed and left absent, so a client that
// ignores Mixed reads "unknown" rather than a value that is true of one constituent.
func (e *GraphEdge) summarize() {
	e.Keys, e.Mixed, e.Locked = []string{}, []string{}, []string{}
	for _, p := range e.parts {
		for _, commit := range p.locked {
			if !slices.Contains(e.Locked, commit) {
				e.Locked = append(e.Locked, commit)
			}
		}
	}
	slices.Sort(e.Locked)
	e.Count = len(e.parts)
	e.Kind, e.Boundary, e.RefStyle = "", "", ""
	e.AdvisoryRoute, e.FindingRoute, e.OwnedDepth = nil, nil, nil
	if len(e.parts) == 0 {
		return
	}

	first := e.parts[0]
	e.Kind, e.Boundary, e.RefStyle = first.kind, first.boundary, first.refStyle
	advisory, finding, depth := first.advisory, first.finding, first.depth
	for _, p := range e.parts[1:] {
		e.Kind = shared(e, edgeFactKind, e.Kind, p.kind)
		e.Boundary = shared(e, edgeFactBoundary, e.Boundary, p.boundary)
		e.RefStyle = shared(e, edgeFactRefStyle, e.RefStyle, p.refStyle)
		if advisory != p.advisory {
			e.mix(edgeFactAdvisory)
		}
		if finding != p.finding {
			e.mix(edgeFactFinding)
		}
		if depth != p.depth {
			e.mix(edgeFactDepth)
		}
	}
	if advisory.Valid && !e.IsMixed(edgeFactAdvisory) {
		e.AdvisoryRoute = &advisory.Bool
	}
	if finding.Valid && !e.IsMixed(edgeFactFinding) {
		e.FindingRoute = &finding.Bool
	}
	if depth.Valid && !e.IsMixed(edgeFactDepth) {
		hops := int(depth.Int64)
		e.OwnedDepth = &hops
	}

	for _, p := range e.parts {
		// Containment carries no key, and a merged edge never mixes it with a declared one.
		if p.key != "" {
			e.Keys = append(e.Keys, p.key)
		}
	}
	sort.Strings(e.Keys)
}

// shared keeps a value only while the relationships agree about it, and records the disagreement
// once they do not.
func shared(e *GraphEdge, fact, running, next string) string {
	if running == next && !e.IsMixed(fact) {
		return running
	}
	e.mix(fact)
	return ""
}

func (e *GraphEdge) mix(fact string) {
	if e.IsMixed(fact) {
		return
	}
	e.Mixed = append(e.Mixed, fact)
}

// Graph is the whole corpus.
type Graph struct {
	Nodes         []GraphNode `json:"nodes"`
	Edges         []GraphEdge `json:"edges"`
	IncludesSteps bool        `json:"includesSteps"`
	// ClientApplied names the predicate terms this payload was not narrowed by, because whoever
	// draws it applies them. The Explorer filters one payload rather than asking again; any other
	// caller has to be told it was handed work rather than having it done.
	ClientApplied []string `json:"clientApplied,omitempty"`
}

// FullGraphOptions selects what the whole-corpus view contains.
type FullGraphOptions struct {
	// IncludeSteps keeps every individual `uses:` line rather than folding it into the
	// workflow or composite action that holds it. Roughly 23k nodes against 3.6k.
	IncludeSteps bool
	// Scope narrows the graph to a reason for looking at it. Empty is the whole corpus.
	Scope string
	// Closure is the repositories the declared scope reaches, as KeepList returns them. Read only
	// under ScopeOwned, and empty there means an empty graph rather than the whole corpus.
	Closure []string
	// Boundary keeps only relationships of one class. Empty keeps every class *and* the
	// unclassified, because absent is not a class to filter on.
	Boundary string
	// EdgeKinds keeps only relationships of these kinds. Empty asks nothing. Containment is
	// unaffected: it is a drawing aid rather than a declared relationship, and filtering it away
	// would answer a question about `needs:` with a scatter of unattached nodes.
	EdgeKinds []string
	// Origins keeps only nodes from repositories of these tiers. A node with no repository reads
	// as "external", which is what the browser calls it. Empty asks nothing.
	Origins []string
	// MinReach is a floor on how many repositories depend on a node. Zero asks nothing; a node
	// whose reach has never been measured meets no floor, which is the direction that avoids
	// claiming a reach nothing established.
	MinReach int
	// UnanalyzedOnly keeps what has not been analyzed at every reference in use.
	UnanalyzedOnly bool
	// RefStyle keeps only nodes some edge names by a commit id (`sha`) or by anything else
	// (`tag`). Empty asks nothing; `any` is the interface's word for the same.
	RefStyle string
	// Mode keeps only what one analysis shape observed. The graph applies it rather than handing
	// it over: a browser cannot filter by a fact the payload does not carry, and the edges the
	// mode excluded are exactly the ones it never received. Empty is `regular`.
	Mode string
	// GroupProperty names the repository property a reader is grouping by. It labels nodes rather
	// than filtering them: grouping is not narrowing, so a graph labeled by a property still draws
	// everything those repositories reach. Empty labels nothing.
	GroupProperty string
	// GroupClosure is the repositories a property filter admits — the owned ones the organization
	// says match, plus everything they reach. Computed rather than asked for, because the sources
	// are what include and exclude act on and the chain leaving them is the answer.
	GroupClosure []string
	// GroupFiltered says a property filter was asked for at all, so a filter matching no repository
	// draws nothing rather than everything. Absent and empty are different questions here too.
	GroupFiltered bool
}

// Reference styles, lexical only: whether a reference is *safe* is a query's verdict, never this
// file's.
const (
	RefStyleSHA = "sha"
	RefStyleTag = "tag"
)

// commitRef matches a full git object name. The same test the browser applies, so the two sides
// cannot disagree about what counts as pinned.
var commitRef = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// refStylesOf summarizes onto each node the styles of the references its edges name.
//
// A property of the edge, but readers filter on nodes, so it is recorded against both ends —
// exactly as `graph.js` does, because a node the browser keeps and the server drops is a filter
// that means two things.
func refStylesOf(edges []GraphEdge) map[string]map[string]bool {
	styles := map[string]map[string]bool{}
	mark := func(key, style string) {
		if styles[key] == nil {
			styles[key] = map[string]bool{}
		}
		styles[key][style] = true
	}
	for _, e := range edges {
		for _, ref := range e.Refs {
			style := RefStyleTag
			if commitRef.MatchString(ref) {
				style = RefStyleSHA
			}
			mark(e.Source, style)
			mark(e.Target, style)
		}
	}
	return styles
}

// fullyAnalyzedNode is the browser's rule: a dependency is analyzed only when every reference in
// use has been, and zero references is nothing looked at rather than everything covered.
func fullyAnalyzedNode(n GraphNode) bool {
	if n.RefsInUse > 0 {
		return n.RefsAnalyzed >= n.RefsInUse
	}
	return false
}

// originTierOf is the tier a node belongs to, with the same fallback the browser applies: a node
// with no repository has no origin to read and counts as external.
func originTierOf(n GraphNode) string {
	if n.Origin == "" {
		return "external"
	}
	return n.Origin
}

// applyNodePredicates drops what the predicate excludes, and the edges left dangling.
//
// **None of these can be pushed into SQL, and measuring on the real corpus is what proved it.**
// The node set is not what the query returns: `splitNodesByRef` derives each per-reference node
// from the *edges* that name a reference, so removing a node before the split removes edges, and
// removing edges removes the references a dependency exists at. Filtering `origins=external` in
// SQL cut the seed and upstream steps that point at external actions, and **1,889 of 3,433 action
// nodes then vanished entirely** — not hidden, absent, because nothing was left to name their
// versions. The three origin buckets stopped partitioning the corpus: 11,045 nodes against 12,550.
//
// The scope filter survives in SQL only because a closure is edge-closed by construction: it
// admits any node a declared repository's edge names, so both ends of those edges are kept. An
// origin is not, and neither is a reach floor.
//
// Applied after owners are attached, because that is the node set the browser applies the same
// predicates to, and an account is judged on its own rolled-up figures exactly as it is on screen.
func applyNodePredicates(nodes []GraphNode, edges []GraphEdge, opts FullGraphOptions) ([]GraphNode, []GraphEdge) {
	style := opts.RefStyle
	if style == "any" {
		style = ""
	}
	if len(opts.Origins) == 0 && opts.MinReach <= 0 && !opts.UnanalyzedOnly && style == "" {
		return nodes, edges
	}
	var styles map[string]map[string]bool
	if style != "" {
		styles = refStylesOf(edges)
	}
	wanted := make(map[string]bool, len(opts.Origins))
	for _, o := range opts.Origins {
		wanted[o] = true
	}
	kept := make([]GraphNode, 0, len(nodes))
	visible := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if len(wanted) > 0 && !wanted[originTierOf(n)] {
			continue
		}
		// An unmeasured reach meets no floor: the alternative claims a reach nothing established.
		if opts.MinReach > 0 && n.DependentRepos < opts.MinReach {
			continue
		}
		if opts.UnanalyzedOnly && fullyAnalyzedNode(n) {
			continue
		}
		if style != "" && !styles[n.Key][style] {
			continue
		}
		kept = append(kept, n)
		visible[n.Key] = true
	}
	surviving := make([]GraphEdge, 0, len(edges))
	for _, e := range edges {
		if visible[e.Source] && visible[e.Target] {
			surviving = append(surviving, e)
		}
	}
	return kept, surviving
}

// ScopeExposure keeps only what lies on a route from a repository under review to an action an
// advisory names.
//
// The route is the unit, not the affected action: whether a fix is yours or an intermediary's is
// a fact about the path, and it is the question the whole graph exists to answer. On the
// reference corpus that is 213 nodes against 9,811, so it is also the cheapest view to draw.
const ScopeExposure = "exposure"

// onExposureRoute is every node key lying on a stored exposure route.
//
// Read from the stored path rather than recomputed, so this scope and the Remediation page
// cannot disagree about what is exposed. abom_exposure is rebuilt wholesale, so an empty result
// means the last rebuild found nothing, not that nothing was looked at.
const onExposureRoute = `(SELECT j.value FROM abom_exposure ex, json_each(ex.path) j)`

// ScopeOwned keeps only what the operator's declaration reaches.
//
// The whole corpus is not a view anybody asked for: it is everything the crawl happened to walk
// through, and at 56,830 nodes no edge can be followed from one end to the other. What an operator
// has a word for is what they own and what it pulls in.
const ScopeOwned = "owned"

// closureCTE is every live node a closure reaches, as a named CTE the scope filter selects from.
//
// A CTE rather than repeating the predicate: the filter appears five times across two queries, and
// binding the closure once per query instead of once per occurrence is the difference between
// three copies of the argument list and fifteen.
//
// The three clauses are the negation of pruneOutsideClosure's deadNodes, deliberately and
// literally. A node is inside when its repository is declared, when it *is* a declared repository,
// or when a live edge belonging to one names it — that last clause is what makes this a closure
// rather than a filter, and re-deriving it here instead of mirroring it is how the picture and the
// pruning come to disagree about what is in scope.
//
// Named, because the owned scope and a property filter each bring their own and a graph asked for
// both must intersect them rather than let one silently win.
func closureCTE(name string, closure []string, mode string) (string, []any) {
	list, names := inList(closure)
	args := append(append(append([]any{}, names...), names...), names...)
	return `, ` + name + ` AS (
		SELECT n.node_key
		  FROM abom_node_live n
		  LEFT JOIN repository r ON r.row_id = n.repository_row_id
		 WHERE LOWER(r.repository_full_name) IN ` + list + `
		    OR LOWER(n.owner || '/' || n.repo) IN ` + list + `
		    OR EXISTS (SELECT 1 FROM ` + edgeSource(mode) + ` e
		                JOIN repository er ON er.row_id = e.repository_row_id
		               WHERE (e.src_node_row_id = n.row_id OR e.dst_node_row_id = n.row_id)
		                 AND LOWER(er.repository_full_name) IN ` + list + `))`, args
}

// scopeParts is the CTEs a query needs and the arguments that bind them. Empty for every scope that
// reads from stored rows, which is why the exposure scope needs neither.
func scopeParts(opts FullGraphOptions) (string, []any) {
	cte, args := "", []any{}
	if opts.Scope == ScopeOwned && len(opts.Closure) > 0 {
		text, a := closureCTE("closure", opts.Closure, opts.Mode)
		cte, args = cte+text, append(args, a...)
	}
	if len(opts.GroupClosure) > 0 {
		text, a := closureCTE("grouped", opts.GroupClosure, opts.Mode)
		cte, args = cte+text, append(args, a...)
	}
	return cte, args
}

// scopeFilter restricts a query to the chosen scope, as a fragment appended to its WHERE clause.
//
// Applied in SQL rather than to the assembled graph: filtering afterwards still paid for all
// 9,811 nodes and their per-node advisory and frontier subqueries in order to return 277.
func scopeFilter(opts FullGraphOptions, column string) string {
	out := ""
	switch opts.Scope {
	case ScopeExposure:
		out += " AND " + column + " IN " + onExposureRoute
	case ScopeOwned:
		// Declaring nothing shows nothing. Falling through to the whole corpus would answer a
		// question about the operator's own supply chain with somebody else's.
		if len(opts.Closure) == 0 {
			return " AND 0"
		}
		out += " AND " + column + " IN (SELECT node_key FROM closure)"
	}
	switch {
	case len(opts.GroupClosure) > 0:
		out += " AND " + column + " IN (SELECT node_key FROM grouped)"
	case opts.GroupFiltered:
		// A filter nobody matches shows nothing, for the reason an undeclared scope does.
		return " AND 0"
	}
	return out
}

// display folds steps and jobs into the container that holds them.
//
// One level is enough: every step and job has a container that is itself neither, so the
// collapsed node count is exactly the count of everything else.
const collapsedDisplay = `disp AS (
	SELECT n.row_id, COALESCE(c.row_id, n.row_id) AS shown
	FROM abom_node_live n
	LEFT JOIN abom_node_live c ON c.row_id = n.container_node_row_id)`

const identityDisplay = `disp AS (SELECT row_id, row_id AS shown FROM abom_node_live)`

// frontierMatch pairs a node with its queue rows.
//
// A repository node is keyed `repo:owner/name` and lowercased by normalize.RepoKey, while its
// queue row is keyed `owner/name` in GitHub's own spelling. An exact match on node_key therefore
// never fired for a repository: on the reference corpus 93 repositories analyzed minutes earlier
// all reported `analyzed = false`, and none carried a discovery state at all.
//
// The repository takes only its **root** row, `depth = 0`, which `expand.SeedOwnedRepositories`
// is the sole producer of. A project the operator owns is two nodes — the repository, and the
// same project as a dependency others use at particular references — and they answer different
// questions. `advanced-security/filter-sarif` is analyzed at its default branch and queued at
// `main` and `v1.1` because somebody else uses those; letting the repository see those rows
// reported the operator's own work as unanalyzed when it was not.
const frontierMatch = `(f.node_key = n.node_key
		OR (n.node_kind = 'repo' AND f.depth = 0
		    AND LOWER(f.node_key) = SUBSTR(n.node_key, 6)))`

// frontierState is a node's place in the discovery queue, split the way a reader reads it.
// Most nodes are not dependencies and have no row; that is an empty string, not a state.
//
// A dependency now has one row per reference, and the states rank so the least settled wins: a
// node with four analyzed refs and one still queued is not analyzed, and reporting it as such
// would hide exactly the coverage gap this split exists to show.
const frontierState = `(SELECT CASE
				 WHEN SUM(f.status = 'running') > 0 THEN 'running'
				 WHEN SUM(f.status = 'pending' AND f.attempts > 0) > 0 THEN 'retrying'
				 WHEN SUM(f.status = 'pending') > 0 THEN 'queued'
				 WHEN SUM(f.status = 'done') > 0 THEN 'done'
				 ELSE MIN(f.status)
			   END FROM abom_frontier f WHERE ` + frontierMatch + `)`

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

	nodes, index, err := s.graphNodes(display, 0, opts)
	if err != nil {
		return out, err
	}
	if err := s.attachRules(display, index, opts.Mode); err != nil {
		return out, err
	}
	edges, err := s.graphEdges(display, 0, opts)
	if err != nil {
		return out, err
	}

	// Before owners are attached, so a version hangs from the same owner its dependency does.
	nodes, edges, err = s.splitNodesByRef(nodes, edges)
	if err != nil {
		return out, err
	}

	// Before owners are attached, so an account holds every repository the operator declared
	// rather than only the ones something has managed to analyze.
	nodes, err = s.attachOwnedRepositories(nodes, opts)
	if err != nil {
		return out, err
	}

	nodes = attachOwners(nodes)

	// Read rather than recomputed. Containment is a fact about the corpus, so a filter narrows
	// what is drawn from it and never adds a holding nothing states.
	held, err := s.Containment()
	if err != nil {
		return out, err
	}
	edges = append(edges, containmentEdges(nodes, held)...)

	nodes, edges = applyNodePredicates(nodes, edges, opts)

	out.Nodes, out.Edges = nodes, edges
	return out, nil
}

// frontierRollup is every frontier column a node needs, aggregated once for the whole query
// instead of re-read per node.
//
// The inner UNION ALL is what makes a single equality join enough. frontierMatch has two arms —
// an exact key, and a repository node keyed `repo:owner/name` against a queue keyed `owner/name`
// — and the second applies LOWER() to the indexed column, so no index can serve it and SQLite
// scans the whole frontier for every node. Emitting the repository form as its own row moves that
// arm to the build side, where it is paid once. `repo:` is five characters, which is the 6 the
// original SUBSTR skipped past.
//
// A row cannot match both arms: one is the frontier key and the other is that key with a prefix,
// so the UNION ALL cannot double count.
const frontierRollup = `, fstate AS (
	SELECT mk,
	       SUM(status = 'running') AS running,
	       SUM(status = 'pending' AND attempts > 0) AS retrying,
	       SUM(status = 'pending') AS pending,
	       SUM(status = 'done') AS done,
	       MIN(status) AS min_status,
	       MIN(depth) AS min_depth,
	       MAX(score) AS max_score,
	       COUNT(*) AS refs
	  FROM (SELECT node_key AS mk, status, attempts, depth, score FROM abom_frontier
	        UNION ALL
	        SELECT 'repo:' || LOWER(node_key), status, attempts, depth, score
	          FROM abom_frontier WHERE depth = 0)
	 GROUP BY mk)`

// advisoryRollup counts advisories per package once rather than per node.
const advisoryRollup = `, adv AS (
	SELECT p.package_name AS pkg, COUNT(DISTINCT a.ghsa_id) AS advisories
	  FROM abom_advisory a
	  JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
	 WHERE a.withdrawn_at IS NULL
	 GROUP BY p.package_name)`

// groupingRollup resolves each owned repository's grouping value once rather than per node. A
// correlated lookup here runs for every node in the graph, which is what the cost guard on this
// query exists to stop.
//
// MIN picks one value for a multi-select property. Grouping needs a node to sit in exactly one
// place, and choosing the first by name is at least stable across rebuilds. The owned-scope
// predicate is the point: a grouping divides what the operator owns, and a property on somebody
// else's repository is their administration rather than a dividing line here.
var groupingRollup = `, grouping AS (
	SELECT p.repository_full_name AS repo, MIN(p.value) AS value
	  FROM abom_repository_property p
	 WHERE p.property_name = ? AND ` + ownedFullName("p.repository_full_name") + `
	 GROUP BY p.repository_full_name)`

// rolledFrontierState reads the same ranking frontierState does, from the rollup. The least
// settled reference still wins, so a node with four analyzed refs and one queued is not analyzed.
const rolledFrontierState = `(CASE
		 WHEN fs.running > 0 THEN 'running'
		 WHEN fs.retrying > 0 THEN 'retrying'
		 WHEN fs.pending > 0 THEN 'queued'
		 WHEN fs.done > 0 THEN 'done'
		 ELSE fs.min_status
	   END)`

// graphNodesQuery is separate from the read so its plan can be asserted: the cost of this query
// is a structural property, not something a stopwatch should be asked about.
func graphNodesQuery(display string, since int64, opts FullGraphOptions) (string, []any) {
	cte, args := scopeParts(opts)
	return `WITH ` + display + cte + frontierRollup + advisoryRollup + groupingRollup + `
		SELECT n.node_key, n.node_kind, COALESCE(r.repository_full_name, ''),
		       COALESCE(r.origin, ''), COALESCE(n.path, ''), COALESCE(c.node_key, ''),
		       COALESCE(n.owner, ''),
		       n.is_external, COALESCE(b.dependent_repos, 0), ` + reachKnown + `,
		       COALESCE(adv.advisories, 0),
		       COALESCE(fs.done, 0) > 0,
		       COALESCE(` + rolledFrontierState + `, ''),
		       COALESCE(fs.min_depth, 0),
		       COALESCE(fs.max_score, 0),
		       COALESCE(fs.refs, 0),
		       COALESCE(fs.done, 0),
		       COALESCE(u.value, '')
		FROM abom_node_live n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		LEFT JOIN abom_node_live c ON c.row_id = n.container_node_row_id
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		LEFT JOIN fstate fs ON fs.mk = n.node_key
		LEFT JOIN adv ON adv.pkg = n.owner || '/' || n.repo
		LEFT JOIN grouping u ON u.repo = r.repository_full_name
		WHERE n.row_id IN (SELECT shown FROM disp WHERE row_id > ?)` +
		scopeFilter(opts, "n.node_key") + nodeModeFilter(opts.Mode), append(args, opts.GroupProperty, since)
}

// graphNodes reads the displayed nodes. A non-zero since restricts them to those a node added
// after that row id displays as, which is how a delta is derived.
func (s *Store) graphNodes(display string, since int64, opts FullGraphOptions) ([]GraphNode, map[string]*GraphNode, error) {
	query, args := graphNodesQuery(display, since, opts)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("reading graph nodes: %w", err)
	}
	defer rows.Close()

	nodes := []GraphNode{}
	for rows.Next() {
		var n GraphNode
		if err := rows.Scan(&n.Key, &n.Kind, &n.Repository, &n.Origin, &n.Path, &n.Container,
			&n.Owner, &n.IsExternal, &n.DependentRepos, &n.ReachKnown, &n.Advisories,
			&n.Analyzed, &n.Discovery, &n.Depth, &n.QueueScore,
			&n.RefsInUse, &n.RefsAnalyzed, &n.Grouping); err != nil {
			return nil, nil, fmt.Errorf("scanning graph node: %w", err)
		}
		n.Rules = []string{}
		n.Facets = []FindingFacet{}
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

// attachRules carries the rule ids that produced each node's findings, and each kind of finding
// whole.
//
// A finding recorded against a step belongs to the workflow once the step is folded away, or
// collapsing would silently drop the evidence along with the node.
func (s *Store) attachRules(display string, index map[string]*GraphNode, mode string) error {
	rows, err := s.db.Query(`WITH ` + display + `
		SELECT shown.node_key, f.rule_id, COALESCE(f.provider, ''), COALESCE(f.severity, ''),
		       NULLIF(f.security_severity, 0), COUNT(*)
		FROM abom_finding_node fn
		JOIN disp d ON d.row_id = fn.node_row_id
		JOIN abom_node_live shown ON shown.row_id = d.shown
		JOIN ` + findingSource(mode) + ` f ON f.row_id = fn.finding_row_id
		GROUP BY shown.node_key, f.rule_id, 3, 4, 5`)
	if err != nil {
		return fmt.Errorf("reading findings by rule: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var facet FindingFacet
		var key string
		var security sql.NullFloat64
		if err := rows.Scan(&key, &facet.Rule, &facet.Provider, &facet.Severity, &security,
			&facet.Count); err != nil {
			return fmt.Errorf("scanning finding rule: %w", err)
		}
		n, ok := index[key]
		if !ok {
			continue
		}
		if security.Valid {
			facet.SecuritySeverity = &security.Float64
		}
		facet.Band = viewspec.BandOf(facet.SecuritySeverity)
		n.Findings += facet.Count
		n.Rules = append(n.Rules, facet.Rule)
		n.Facets = append(n.Facets, facet)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading findings by rule: %w", err)
	}
	for _, n := range index {
		n.Rules = dedupeSorted(n.Rules)
		sortFacets(n.Facets)
	}
	return nil
}

func (s *Store) graphEdges(display string, since int64, opts FullGraphOptions) ([]GraphEdge, error) {
	// Read before the edges, because the store holds one connection and a query cannot be asked
	// while another's rows are open.
	locked, err := s.lockedCommitsByEdgeKey()
	if err != nil {
		return nil, err
	}
	cte, args := scopeParts(opts)
	rows, err := s.db.Query(`WITH `+display+cte+`
		SELECT sn.node_key, dn.node_key, e.edge_kind, COALESCE(e.ref_value, ''),
		       COALESCE(e.boundary, ''), e.edge_key, COALESCE(e.ref_style, ''),
		       e.advisory_route, e.finding_route, e.owned_depth
		FROM `+edgeSource(opts.Mode)+` e
		JOIN disp ds ON ds.row_id = e.src_node_row_id
		JOIN disp dd ON dd.row_id = e.dst_node_row_id
		JOIN abom_node_live sn ON sn.row_id = ds.shown
		JOIN abom_node_live dn ON dn.row_id = dd.shown
		WHERE ds.shown <> dd.shown AND e.row_id > ?`+
		boundaryFilter(opts.Boundary)+edgeKindFilter(opts.EdgeKinds)+
		scopeFilter(opts, "sn.node_key")+scopeFilter(opts, "dn.node_key"),
		append(args, since)...)
	if err != nil {
		return nil, fmt.Errorf("reading graph edges: %w", err)
	}
	defer rows.Close()

	merged := map[[2]string]*GraphEdge{}
	order := [][2]string{}
	for rows.Next() {
		var src, dst string
		var p edgePart
		if err := rows.Scan(&src, &dst, &p.kind, &p.ref, &p.boundary, &p.key, &p.refStyle,
			&p.advisory, &p.finding, &p.depth); err != nil {
			return nil, fmt.Errorf("scanning graph edge: %w", err)
		}
		p.locked = locked[p.key]
		key := [2]string{src, dst}
		e, ok := merged[key]
		if !ok {
			e = &GraphEdge{Source: src, Target: dst, Refs: []string{}}
			merged[key] = e
			order = append(order, key)
		}
		e.parts = append(e.parts, p)
		e.addGraphRef(p.ref)
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
		e.summarize()
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
