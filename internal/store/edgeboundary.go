package store

import "fmt"

// What kind of boundary a `uses:` crosses.
//
// Three different pieces of work and three different risks. A workflow calling a reusable
// workflow in its own repository is a refactor away from being inlined; the same call across two
// repositories of one account is coordination inside a team; the same call to another account is
// a dependency on a third party. Deliberately not a fourth value for "owned versus not" — that is
// a property of the operator's declaration, changes when the declaration changes, and belongs to
// the bill of materials rather than to the edge. These three are properties of the world.
const (
	BoundarySameRepository = "same-repository"
	BoundarySameOwner      = "same-owner"
	BoundaryCrossOwner     = "cross-owner"
)

// targetRepository is the repository an edge points at.
//
// Read from the node's own owner and repo first, because a node upsert never revises
// repository_row_id and a repository first seen as somebody else's dependency has none. Requires
// the target node aliased `dst`.
const targetRepository = `COALESCE(
	NULLIF(dst.owner || '/' || dst.repo, '/'),
	(SELECT tr.repository_full_name FROM repository tr WHERE tr.row_id = dst.repository_row_id))`

// ClassifyEdgeBoundaries fills the boundary on every edge that has none, and reports how many.
//
// Derivable entirely from rows already stored, so existing edges are filled by this rather than
// by re-analysis. Idempotent: it settles to zero once the classification and the graph agree,
// which is what lets it run after every ingest without a second mechanism deciding when it is
// due.
func (s *Store) ClassifyEdgeBoundaries() (int, error) {
	// An edge whose containing repository or whose target cannot be named has an *unknown*
	// boundary, not a cross-owner one: reporting a third-party dependency nobody observed is an
	// absent measurement drawn as a measurement.
	//
	// Restricted to live edges, so pruned stores do not classify tombstones. The endpoint is read
	// from `abom_node` because the boundary is structural, whether or not the target is drawn.
	res, err := s.db.Exec(`
		UPDATE abom_edge SET boundary = (
			SELECT CASE
			         WHEN LOWER(target) = LOWER(container) THEN ?
			         WHEN LOWER(SUBSTR(target, 1, INSTR(target, '/') - 1))
			              = LOWER(SUBSTR(container, 1, INSTR(container, '/') - 1)) THEN ?
			         ELSE ?
			       END
			FROM (SELECT `+targetRepository+` AS target,
			             r.repository_full_name  AS container
			        FROM abom_node dst
			        LEFT JOIN repository r ON r.row_id = abom_edge.repository_row_id
			       WHERE dst.row_id = abom_edge.dst_node_row_id)
			 WHERE target IS NOT NULL AND container IS NOT NULL)
		WHERE boundary IS NULL
		  AND row_id IN (SELECT row_id FROM abom_edge_live)`,
		BoundarySameRepository, BoundarySameOwner, BoundaryCrossOwner)
	if err != nil {
		return 0, fmt.Errorf("classifying edge boundaries: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting classified edges: %w", err)
	}

	// RowsAffected counts every row the statement touched, including those left NULL because
	// nothing could name their target. Those are not classified and must not be reported as such,
	// or the pass never settles and its own idempotence test would be measuring the wrong thing.
	var unresolved int64
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM abom_edge_live WHERE boundary IS NULL`).Scan(&unresolved); err != nil {
		return 0, fmt.Errorf("counting unclassifiable edges: %w", err)
	}
	return int(changed - unresolved), nil
}

// boundaryFilter restricts a query to one boundary class, as a fragment appended to its WHERE
// clause. Requires the edge aliased `e`.
//
// Only the three known classes produce a fragment. An unrecognized value filters nothing rather
// than reaching the SQL, so a query string cannot widen into the statement; and the empty string
// keeps the unclassified too, because absent is not a class to filter on.
func boundaryFilter(boundary string) string {
	switch boundary {
	case BoundarySameRepository, BoundarySameOwner, BoundaryCrossOwner:
		return " AND e.boundary = '" + boundary + "'"
	default:
		return ""
	}
}
