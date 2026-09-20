package store

import "strings"

// What a relationship is, as the extraction records it.
//
// EdgeKindJobNeedsJob is the only edge in the graph that is ordering rather than use, and the
// distinction matters because reach means "how many repositories would be affected if this were
// compromised". A job in your own workflow is not a component you depend on, so counting a
// `needs:` edge as a dependency inflates blast radius and lists jobs among an organization's
// supply chain.
const (
	EdgeKindStepUsesAction          = "step-uses-action"
	EdgeKindJobNeedsJob             = "job-needs-job"
	EdgeKindJobUsesReusableWorkflow = "job-uses-reusable-workflow"
	EdgeKindCompositeStepUsesAction = "composite-step-uses-action"
)

// DependencyEdgeFilter restricts a traversal to edges that mean "uses", written once so the
// aggregates cannot disagree about what a dependency is. Requires the edge table aliased `e`.
const DependencyEdgeFilter = `e.edge_kind <> '` + EdgeKindJobNeedsJob + `'`

// EdgeKinds is the vocabulary a reader may filter by, kept beside the filter that applies it.
func EdgeKinds() []string {
	return []string{
		EdgeKindStepUsesAction, EdgeKindJobNeedsJob,
		EdgeKindJobUsesReusableWorkflow, EdgeKindCompositeStepUsesAction,
	}
}

// edgeKindFilter restricts a query to the kinds of relationship asked for, as a fragment appended
// to its WHERE clause. Requires the edge aliased `e`.
//
// Only the kinds the extraction produces reach the SQL, so a query string cannot widen into the
// statement. A value outside them narrows to nothing rather than to everything: a kind ABOM never
// records has an empty answer, and returning the whole corpus instead is the failure
// `boundaryFilter` was caught in — the unfiltered graph arriving under a promise it was narrowed.
func edgeKindFilter(kinds []string) string {
	if len(kinds) == 0 {
		return ""
	}
	known := map[string]bool{}
	for _, k := range EdgeKinds() {
		known[k] = true
	}
	wanted := make([]string, 0, len(kinds))
	for _, k := range kinds {
		if known[k] {
			wanted = append(wanted, "'"+k+"'")
		}
	}
	if len(wanted) == 0 {
		return " AND 1 = 0"
	}
	return " AND e.edge_kind IN (" + strings.Join(wanted, ", ") + ")"
}
