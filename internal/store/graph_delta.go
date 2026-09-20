package store

import (
	"database/sql"
	"fmt"
	"time"
)

// GraphVersion is the cheap poll an interface makes to learn whether the graph moved.
type GraphVersion struct {
	Generation int64  `json:"generation"`
	Nodes      int    `json:"nodes"`
	Edges      int    `json:"edges"`
	UpdatedAt  string `json:"updatedAt"`
	// ReachRecomputedAt is when blast radius was last derived. Nodes added after it carry no
	// measured reach, which is why the interface has to say how old this is.
	ReachRecomputedAt string `json:"reachRecomputedAt"`
}

// GraphRebuild records what a generation cost to produce.
type GraphRebuild struct {
	// ReachRebuilt reports whether this generation recomputed blast radius, as opposed to only
	// ingesting.
	ReachRebuilt bool
	Duration     time.Duration
	Nodes        int
	Edges        int
}

// GraphMetric is the part of a node that changes without the node itself changing.
type GraphMetric struct {
	Key            string `json:"key"`
	DependentRepos int    `json:"dependentRepos"`
	ReachKnown     bool   `json:"reachKnown"`
	Findings       int    `json:"findings"`
	Advisories     int    `json:"advisories"`
	Analyzed       bool   `json:"analyzed"`
	// Coverage moves as references are analyzed, without the node itself changing, which is the
	// same reason reach is carried here.
	RefsInUse    int `json:"refsInUse"`
	RefsAnalyzed int `json:"refsAnalyzed"`
	// Discovery moves as the queue is worked, which is the whole reason a follower refreshes.
	Discovery string `json:"discovery,omitempty"`
}

// GraphDelta is what changed since a generation the client already holds.
//
// Nodes and edges are candidates rather than strictly new: a step added under a workflow that is
// already on screen displays as that workflow. The client merges by key, so a repeat updates
// rather than duplicates.
type GraphDelta struct {
	Generation int64 `json:"generation"`
	// Reset tells the client its cursor cannot be honored and it must load the graph again.
	Reset   bool          `json:"reset"`
	Nodes   []GraphNode   `json:"nodes"`
	Edges   []GraphEdge   `json:"edges"`
	Metrics []GraphMetric `json:"metrics"`
	// RemovedNodes and RemovedEdges name what disappeared since the cursor. A delta that could
	// only ever add left a client holding a pruned node for as long as the page stayed open.
	RemovedNodes []string `json:"removedNodes"`
	RemovedEdges []string `json:"removedEdges"`
}

// ReachStaleness reports how far the stored blast radius has fallen behind the graph.
type ReachStaleness struct {
	// Never is true when reach has not been derived at all, which no amount of waiting fixes.
	Never bool
	At    time.Time
	// NewEdges counts dependencies ingested since, and TotalEdges the corpus they joined.
	NewEdges   int
	TotalEdges int
}

// ReachStaleness measures the gap a rebuild would close, so the decision to pay for one is made
// against the graph rather than a timer alone.
func (s *Store) ReachStaleness() (ReachStaleness, error) {
	out := ReachStaleness{Never: true}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_edge_regular`).Scan(&out.TotalEdges); err != nil {
		return out, fmt.Errorf("counting edges: %w", err)
	}

	var at sql.NullString
	var mark sql.NullInt64
	err := s.db.QueryRow(`
		SELECT created_at, edge_watermark FROM abom_graph_generation
		WHERE reach_rebuilt = 1 ORDER BY generation DESC LIMIT 1`).Scan(&at, &mark)
	if err == sql.ErrNoRows {
		out.NewEdges = out.TotalEdges
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("reading the last reach rebuild: %w", err)
	}

	parsed, err := time.Parse(time.RFC3339Nano, at.String)
	if err != nil {
		return out, fmt.Errorf("parsing rebuild time %q: %w", at.String, err)
	}
	out.Never, out.At = false, parsed
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM abom_edge_regular WHERE row_id > ?`, mark.Int64).Scan(&out.NewEdges); err != nil {
		return out, fmt.Errorf("counting edges since the last rebuild: %w", err)
	}
	return out, nil
}

// GraphVersion reports the current generation without reading the graph.
func (s *Store) GraphVersion() (GraphVersion, error) {
	out := GraphVersion{}
	var created, reach sql.NullString
	err := s.db.QueryRow(`
		SELECT COALESCE(MAX(generation), 0),
		       MAX(created_at),
		       MAX(CASE WHEN reach_rebuilt = 1 THEN created_at END)
		FROM abom_graph_generation`).Scan(&out.Generation, &created, &reach)
	if err != nil {
		return out, fmt.Errorf("reading graph version: %w", err)
	}
	out.UpdatedAt, out.ReachRecomputedAt = created.String, reach.String

	for _, c := range []struct {
		query string
		into  *int
	}{
		{`SELECT COUNT(*) FROM abom_node_live`, &out.Nodes},
		{`SELECT COUNT(*) FROM abom_edge_regular`, &out.Edges},
	} {
		if err := s.db.QueryRow(c.query).Scan(c.into); err != nil {
			return out, fmt.Errorf("reading graph version: %w", err)
		}
	}
	return out, nil
}

// RecordGraphGeneration marks the graph as it stands now, returning the new generation.
func (s *Store) RecordGraphGeneration(r GraphRebuild) (int64, error) {
	var nodeMark, edgeMark sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(row_id) FROM abom_node_live`).Scan(&nodeMark); err != nil {
		return 0, fmt.Errorf("reading node watermark: %w", err)
	}
	if err := s.db.QueryRow(`SELECT MAX(row_id) FROM abom_edge_regular`).Scan(&edgeMark); err != nil {
		return 0, fmt.Errorf("reading edge watermark: %w", err)
	}
	var containMark sql.NullInt64
	if err := s.db.QueryRow(
		`SELECT MAX(row_id) FROM abom_containment`).Scan(&containMark); err != nil {
		return 0, fmt.Errorf("reading containment watermark: %w", err)
	}
	// Carried forward rather than left to the column default. A generation written at 0 after a
	// compaction has bumped the epoch can never match the current one, so the delta answers reset
	// for the life of the database and every open page reloads the whole payload on every poll.
	epoch, err := s.currentEpoch()
	if err != nil {
		return 0, err
	}

	var ms any
	if r.Duration > 0 {
		ms = r.Duration.Milliseconds()
	}
	res, err := s.db.Exec(`
		INSERT INTO abom_graph_generation
			(node_watermark, edge_watermark, containment_watermark, created_at, reach_rebuilt,
			 rebuild_ms, rebuilt_nodes, rebuilt_edges, epoch)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nodeMark.Int64, edgeMark.Int64, containMark.Int64, now(), r.ReachRebuilt,
		ms, r.Nodes, r.Edges, epoch)
	if err != nil {
		return 0, fmt.Errorf("recording graph generation: %w", err)
	}
	return res.LastInsertId()
}

// currentEpoch is the epoch new generations belong to, which only the compaction advances.
//
// One reader for both writers: a second copy of this query is how the two drifted apart in the
// first place, with the compaction naming the column and the ingestion path leaving it defaulted.
func (s *Store) currentEpoch() (int64, error) {
	var epoch int64
	if err := s.db.QueryRow(
		`SELECT COALESCE(MAX(epoch), 0) FROM abom_graph_generation`).Scan(&epoch); err != nil {
		return 0, fmt.Errorf("reading the epoch: %w", err)
	}
	return epoch, nil
}

// GraphDelta returns what changed since a generation, or asks for a full reload when that
// generation is not on record.
func (s *Store) GraphDelta(since int64, opts FullGraphOptions) (GraphDelta, error) {
	out := GraphDelta{
		Nodes: []GraphNode{}, Edges: []GraphEdge{}, Metrics: []GraphMetric{},
		RemovedNodes: []string{}, RemovedEdges: []string{},
	}

	version, err := s.GraphVersion()
	if err != nil {
		return out, err
	}
	out.Generation = version.Generation

	var nodeMark, edgeMark int64
	var containMark int64
	var epoch int64
	err = s.db.QueryRow(`
		SELECT node_watermark, edge_watermark, containment_watermark, epoch
		  FROM abom_graph_generation
		 WHERE generation = ?`, since).Scan(&nodeMark, &edgeMark, &containMark, &epoch)
	if err == sql.ErrNoRows {
		out.Reset = true
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("reading generation %d: %w", since, err)
	}
	// Compaction deletes rows, so a cursor from before it names row ids that no longer mean what
	// the client thinks. Nothing short of a reload is honest here.
	var current int64
	if err := s.db.QueryRow(
		`SELECT COALESCE(MAX(epoch), 0) FROM abom_graph_generation`).Scan(&current); err != nil {
		return out, fmt.Errorf("reading the epoch: %w", err)
	}
	if current != epoch {
		out.Reset = true
		return out, nil
	}

	if out.RemovedNodes, err = s.removedSince("abom_node", since); err != nil {
		return out, err
	}
	if out.RemovedEdges, err = s.removedSince("abom_edge", since); err != nil {
		return out, err
	}

	display := collapsedDisplay
	if opts.IncludeSteps {
		display = identityDisplay
	}

	nodes, index, err := s.graphNodes(display, nodeMark, opts)
	if err != nil {
		return out, err
	}
	if err := s.attachRules(display, index); err != nil {
		return out, err
	}
	edges, err := s.graphEdges(display, edgeMark, opts)
	if err != nil {
		return out, err
	}
	out.Nodes = attachOwners(nodes)
	held, err := s.containmentSince(containMark)
	if err != nil {
		return out, err
	}
	// Drawn against the whole current projection rather than against the arrivals. A holding can
	// change without a node arriving — declaring an account whose repositories are already on
	// screen states what holds them and adds nothing else — and resolved against the arrivals
	// alone that holding names a key this delta does not carry, so no edge is emitted and the
	// open page never agrees with a reload. The extra read is paid only when a holding moved.
	drawnAgainst := out.Nodes
	if len(held) > 0 {
		all, _, err := s.graphNodes(display, 0, opts)
		if err != nil {
			return out, err
		}
		drawnAgainst = attachOwners(all)
	}
	out.Edges = append(edges, containmentEdges(drawnAgainst, held)...)
	// The same predicate the full payload answered, or an arrival the reader's filter excludes
	// merges into their picture anyway.
	out.Nodes, out.Edges = applyNodePredicates(out.Nodes, out.Edges, opts)

	out.Metrics, err = s.graphMetrics(display)
	if err != nil {
		return out, err
	}
	return out, nil
}

// graphMetrics carries the counts that change for nodes the client already holds. Reach is
// recomputed wholesale, so there is no cheaper way to know which of them moved.
func (s *Store) graphMetrics(display string) ([]GraphMetric, error) {
	rows, err := s.db.Query(`WITH ` + display + `
		SELECT n.node_key, COALESCE(b.dependent_repos, 0), ` + reachKnown + `,
		       (SELECT COUNT(*) FROM abom_finding_node fn
		         JOIN disp d ON d.row_id = fn.node_row_id
		        WHERE d.shown = n.row_id),
		       (SELECT COUNT(DISTINCT a.ghsa_id)
		          FROM abom_advisory a
		          JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		         WHERE a.withdrawn_at IS NULL
		           AND n.owner IS NOT NULL AND n.repo IS NOT NULL
		           AND p.package_name = n.owner || '/' || n.repo),
		       EXISTS (SELECT 1 FROM abom_frontier f
		                WHERE f.node_key = n.node_key AND f.status = 'done'),
		       COALESCE(` + frontierState + `, ''),
		       (SELECT COUNT(*) FROM abom_frontier f WHERE f.node_key = n.node_key),
		       (SELECT COUNT(*) FROM abom_frontier f
		         WHERE f.node_key = n.node_key AND f.status = 'done')
		FROM abom_node_live n
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		WHERE n.row_id IN (SELECT shown FROM disp)`)
	if err != nil {
		return nil, fmt.Errorf("reading graph metrics: %w", err)
	}
	defer rows.Close()

	out := []GraphMetric{}
	for rows.Next() {
		var m GraphMetric
		if err := rows.Scan(&m.Key, &m.DependentRepos, &m.ReachKnown, &m.Findings,
			&m.Advisories, &m.Analyzed, &m.Discovery, &m.RefsInUse, &m.RefsAnalyzed); err != nil {
			return nil, fmt.Errorf("scanning graph metric: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
