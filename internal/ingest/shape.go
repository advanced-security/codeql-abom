package ingest

import (
	"fmt"

	"github.com/advanced-security/codeql-abom/internal/runner"
)

// Shape is how a query's results must be read.
//
// There are deliberately few. A query joins ABOM by declaring one of these, and is then ingested
// with no Go written for it; anything that declares none is refused rather than guessed at,
// because a silently mis-ingested result is worse than one that never arrives.
type Shape string

const (
	// ShapeEdges is the dependency-edge column contract, emitted from `#select`.
	ShapeEdges Shape = "edges"
	// ShapeObservations is the generic subject/name/value contract, emitted from `#select`.
	ShapeObservations Shape = "observations"
	// ShapeGraph is a `@kind graph` query, read from its `nodes` and `edges` result sets.
	ShapeGraph Shape = "graph"
	// ShapeFindings is a problem or path-problem query, read as SARIF via `database analyze`.
	ShapeFindings Shape = "findings"
)

// Tags a query uses to declare which `#select` contract it satisfies. A query kind alone cannot
// say this: a table query has no `@kind` at all, which is exactly why it needs a tag.
const (
	TagEdges       = "abom-edges"
	TagObservation = "abom-observation"
)

// ShapeOf decides how to read a query's results from what the query says about itself.
//
// Nothing here inspects a filename or a path. The engine is meant to be reprogrammed, so the
// query is the only thing that can be trusted to know what it produces.
func ShapeOf(md runner.Metadata) (Shape, error) {
	switch md.Kind {
	case "problem", "path-problem":
		return ShapeFindings, nil
	case "graph":
		return ShapeGraph, nil
	case "":
		switch {
		case md.HasTag(TagEdges):
			return ShapeEdges, nil
		case md.HasTag(TagObservation):
			return ShapeObservations, nil
		}
		return "", fmt.Errorf(
			"query %q declares no @kind and neither the %q nor the %q tag, so there is no way to "+
				"know how to read its results", md.ID, TagEdges, TagObservation)
	default:
		return "", fmt.Errorf("query %q declares @kind %q, which ABOM cannot ingest",
			md.ID, md.Kind)
	}
}
