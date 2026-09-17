package store

// EdgeKindJobNeedsJob records that one job must run after another.
//
// It is the only edge in the graph that is ordering rather than use, and the distinction matters
// because reach means "how many repositories would be affected if this were compromised". A job
// in your own workflow is not a component you depend on, so counting a `needs:` edge as a
// dependency inflates blast radius and lists jobs among an organization's supply chain.
const EdgeKindJobNeedsJob = "job-needs-job"

// DependencyEdgeFilter restricts a traversal to edges that mean "uses", written once so the
// aggregates cannot disagree about what a dependency is. Requires the edge table aliased `e`.
const DependencyEdgeFilter = `e.edge_kind <> '` + EdgeKindJobNeedsJob + `'`
