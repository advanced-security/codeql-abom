package store

import "fmt"

// How a reference names the thing it points at.
//
// Three styles because they are three different guarantees. A commit is immutable, so what was
// analyzed is what will run; a tag is conventionally immutable and can be moved by whoever owns
// it; a branch is expected to move and says nothing about what ran yesterday. Deliberately not
// the pinned/floating pair the coverage counts use: that is a judgment about a repository's
// practice, and this is a reading of the reference itself.
const (
	EdgeRefCommit = "commit"
	EdgeRefTag    = "tag"
	EdgeRefBranch = "branch"
)

// maxOwnedHops bounds the depth walk. The graph admits cycles through mutual references, and a
// depth that keeps increasing would never settle without one.
const maxOwnedHops = 16

// edgeUnits collapses both endpoints of every live edge to the container a reference can name,
// which is the form the exposure paths and the interface are both expressed in. A step is an
// implementation detail of the workflow holding it and nothing references one.
const edgeUnits = `
unit(node_id, unit_id, unit_key) AS (
	SELECT n.row_id, COALESCE(n.container_node_row_id, n.row_id),
	       COALESCE(c.node_key, n.node_key)
	  FROM abom_node_live n
	  LEFT JOIN abom_node_live c ON c.row_id = n.container_node_row_id
),
edge(row_id, ref_value, src_unit, dst_unit, src_unit_key, dst_unit_key, target) AS (
	SELECT e.row_id, e.ref_value, su.unit_id, du.unit_id, su.unit_key, du.unit_key, ` +
	targetRepository + `
	  FROM abom_edge_live e
	  JOIN unit su ON su.node_id = e.src_node_row_id
	  JOIN unit du ON du.node_id = e.dst_node_row_id
	  JOIN abom_node_live dst ON dst.row_id = e.dst_node_row_id
)`

// DeriveEdgeFacts fills the facts that are properties of an edge rather than of its endpoints,
// and reports how many edges changed.
//
// Derivable entirely from rows already stored, so existing edges are filled by this rather than
// by re-analysis. It runs after exposure in the aggregate pass because the advisory fact is read
// from the routes exposure stored: walking again would let this column and the Remediation page
// disagree about what is exposed.
//
// Every fact is tri-state. NULL means nothing computed it — a corpus nobody has analyzed reports
// no route to a finding rather than reporting every edge clean, and an edge ingested since the
// last pass says nothing at all until this runs again.
//
// Idempotent: only rows whose stored value differs are written, so a second pass reports zero.
func (s *Store) DeriveEdgeFacts() (int, error) {
	res, err := s.db.Exec(`
		WITH RECURSIVE `+edgeUnits+`,
		-- Consecutive pairs of the routes exposure recorded, which is what "on a route to a live
		-- advisory" means: the same walk, read rather than repeated.
		route(src_key, dst_key) AS (
			SELECT a.value, b.value
			  FROM abom_exposure ex, json_each(ex.path) a, json_each(ex.path) b
			 WHERE b.key = a.key + 1
		),
		-- Units carrying a finding, and everything that reaches one through a dependency. Ordering
		-- is not use, so a route may not be extended through a needs: edge.
		flagged(unit_id) AS (
			SELECT DISTINCT u.unit_id
			  FROM abom_finding_node fn
			  JOIN unit u ON u.node_id = fn.node_row_id
			UNION
			SELECT su.unit_id
			  FROM flagged f
			  JOIN unit du ON du.unit_id = f.unit_id
			  JOIN abom_edge_live e
			    ON e.dst_node_row_id = du.node_id AND `+DependencyEdgeFilter+`
			  JOIN unit su ON su.node_id = e.src_node_row_id
		),
		-- How far each unit sits from a repository the operator declared. Zero is the declared
		-- repository itself, so an edge declared in it is one hop — the same meaning exposure
		-- gives hops, where one is "names the dependency itself".
		owned(unit_id, hops) AS (
			SELECT DISTINCT u.unit_id, 0
			  FROM abom_node_live n
			  JOIN unit u ON u.node_id = n.row_id
			  LEFT JOIN repository r ON r.row_id = n.repository_row_id
			 WHERE `+ownedFullName(`COALESCE(NULLIF(n.owner || '/' || n.repo, '/'),
			                                  r.repository_full_name)`)+`
			UNION
			SELECT du.unit_id, o.hops + 1
			  FROM owned o
			  JOIN unit su ON su.unit_id = o.unit_id
			  JOIN abom_edge_live e
			    ON e.src_node_row_id = su.node_id AND `+DependencyEdgeFilter+`
			  JOIN unit du ON du.node_id = e.dst_node_row_id
			 WHERE o.hops < ?
		),
		facts(row_id, ref_style, advisory_route, finding_route, owned_depth) AS (
			SELECT e.row_id,
			       CASE
			         WHEN COALESCE(e.ref_value, '') = '' THEN NULL
			         -- Strongest evidence first: a full hex object name needs nothing else on
			         -- record to be readable, and no tag or branch may look like one.
			         WHEN LENGTH(e.ref_value) = 40
			              AND e.ref_value NOT GLOB '*[^0-9a-fA-F]*' THEN ?
			         WHEN e.ref_value GLOB 'refs/tags/*' THEN ?
			         WHEN e.ref_value GLOB 'refs/heads/*' THEN ?
			         WHEN EXISTS (SELECT 1 FROM abom_ref_resolution x
			                       WHERE LOWER(x.owner || '/' || x.repo) = LOWER(e.target)
			                         AND x.tag = e.ref_value) THEN ?
			         WHEN LOWER(e.ref_value) = LOWER((SELECT NULLIF(r.default_branch, '')
			                                            FROM repository r
			                                           WHERE LOWER(r.repository_full_name)
			                                                 = LOWER(e.target))) THEN ?
			         -- A version-shaped reference is a release tag by universal convention. Last,
			         -- so anything measured outranks it, and no other shape is guessed at: wip is
			         -- a tag or a branch and nothing on record says which.
			         WHEN e.ref_value GLOB 'v[0-9]*' OR e.ref_value GLOB '[0-9]*' THEN ?
			       END,
			       CASE
			         WHEN NOT EXISTS (SELECT 1 FROM abom_advisory WHERE withdrawn_at IS NULL)
			              THEN NULL
			         WHEN EXISTS (SELECT 1 FROM route
			                       WHERE route.src_key = e.src_unit_key
			                         AND route.dst_key = e.dst_unit_key) THEN 1
			         ELSE 0
			       END,
			       CASE
			         WHEN NOT EXISTS (SELECT 1 FROM abom_finding_node) THEN NULL
			         WHEN e.dst_unit IN (SELECT unit_id FROM flagged) THEN 1
			         ELSE 0
			       END,
			       (SELECT MIN(o.hops) + 1 FROM owned o WHERE o.unit_id = e.src_unit)
			  FROM edge e
		)
		UPDATE abom_edge
		   SET ref_style      = facts.ref_style,
		       advisory_route = facts.advisory_route,
		       finding_route  = facts.finding_route,
		       owned_depth    = facts.owned_depth
		  FROM facts
		 WHERE facts.row_id = abom_edge.row_id
		   AND (abom_edge.ref_style      IS NOT facts.ref_style
		     OR abom_edge.advisory_route IS NOT facts.advisory_route
		     OR abom_edge.finding_route  IS NOT facts.finding_route
		     OR abom_edge.owned_depth    IS NOT facts.owned_depth)`,
		maxOwnedHops, EdgeRefCommit, EdgeRefTag, EdgeRefBranch, EdgeRefTag, EdgeRefBranch,
		EdgeRefTag)
	if err != nil {
		return 0, fmt.Errorf("deriving edge facts: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting edges whose facts changed: %w", err)
	}
	return int(changed), nil
}
