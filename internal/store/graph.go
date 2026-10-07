package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// DefaultBatchSize is the number of rows buffered before a flush. Large enough to amortize
// transaction overhead, small enough that a failed unit discards little work.
const DefaultBatchSize = 2000

// Node is a graph vertex awaiting insert.
type Node struct {
	Key          string
	Kind         string
	Owner        string
	Repo         string
	Path         string
	JobID        string
	StepIndex    int
	DisplayName  string
	IsExternal   bool
	RepositoryID int64

	// ContainerKey names the workflow or composite action this node belongs to. It must be
	// added before this node so its id is resolvable at flush time.
	ContainerKey string
}

// Edge is a dependency relationship between two nodes, addressed by node key.
//
// It carries raw extraction only. Whether the reference is pinned, vulnerable or otherwise
// risky is recorded as findings keyed by rule id, never as a field here.
type Edge struct {
	Key          string
	SrcKey       string
	DstKey       string
	Kind         string
	RefRaw       string
	RefValue     string
	PinComment   string
	FilePath     string
	StartLine    int
	StartColumn  int
	EndLine      int
	EndColumn    int
	RepositoryID int64
	QueryRunID   int64
	Provenance   string
}

// Writer accumulates graph rows and flushes them in batches.
//
// Node row IDs are resolved through a cache because edges reference nodes by generated ID; the
// cache turns what would be two round-trips per edge endpoint into one lookup per distinct node.
type Writer struct {
	store     *Store
	batchSize int

	nodes []Node
	edges []Edge

	// nodeIDs holds every node key seen since the last Reset. Entries for repository-local
	// nodes are dropped by ResetLocal so the map stays bounded by the largest single
	// repository rather than growing with the whole organization.
	nodeIDs map[string]int64
	// external records which cached keys are shared across repositories and must survive Reset.
	external map[string]bool
}

// NewWriter returns a writer bound to the store. A batchSize of zero uses DefaultBatchSize.
func NewWriter(s *Store, batchSize int) *Writer {
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}
	return &Writer{
		store:     s,
		batchSize: batchSize,
		nodeIDs:   make(map[string]int64),
		external:  make(map[string]bool),
	}
}

// AddNode queues a node. Nodes are deduplicated by key on flush.
func (w *Writer) AddNode(n Node) error {
	w.nodes = append(w.nodes, n)
	if n.IsExternal {
		w.external[n.Key] = true
	}
	if len(w.nodes) >= w.batchSize {
		return w.flushNodes()
	}
	return nil
}

// AddEdge queues an edge. Both endpoints must have been added as nodes before Flush.
func (w *Writer) AddEdge(e Edge) error {
	w.edges = append(w.edges, e)
	if len(w.edges) >= w.batchSize {
		return w.Flush()
	}
	return nil
}

// Flush writes all buffered nodes and then all buffered edges.
func (w *Writer) Flush() error {
	if err := w.flushNodes(); err != nil {
		return err
	}
	return w.flushEdges()
}

// ResetLocal discards cached IDs for repository-scoped nodes, keeping shared external ones.
// Call it between repositories to keep memory flat across a large organization.
func (w *Writer) ResetLocal() {
	for key := range w.nodeIDs {
		if !w.external[key] {
			delete(w.nodeIDs, key)
		}
	}
}

func (w *Writer) flushNodes() error {
	if len(w.nodes) == 0 {
		return nil
	}
	pending := w.nodes
	w.nodes = nil
	keys := make([]string, len(pending))
	for i, n := range pending {
		keys[i] = n.Key
	}
	revived := false

	if err := w.inTx(func(tx *sql.Tx) error {
		var err error
		if revived, err = anyRemoved(tx, "abom_node", "node_key", keys); err != nil {
			return err
		}
		// A run naming a pruned node is the world saying it exists, so it comes back.
		insert, err := tx.Prepare(`
			INSERT INTO abom_node
				(node_key, node_kind, owner, repo, path, job_id, step_index,
				 display_name, is_external, repository_row_id, container_node_row_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(node_key) DO UPDATE SET
				container_node_row_id = COALESCE(excluded.container_node_row_id,
				                                 abom_node.container_node_row_id),
				removed_at = NULL, removed_generation = NULL`)
		if err != nil {
			return fmt.Errorf("preparing node insert: %w", err)
		}
		defer insert.Close()

		lookup, err := tx.Prepare(`SELECT row_id FROM abom_node WHERE node_key = ?`)
		if err != nil {
			return fmt.Errorf("preparing node lookup: %w", err)
		}
		defer lookup.Close()

		for _, n := range pending {
			if _, cached := w.nodeIDs[n.Key]; cached {
				continue
			}
			if _, err := insert.Exec(n.Key, n.Kind, nullable(n.Owner), nullable(n.Repo),
				nullable(n.Path), nullable(n.JobID), n.StepIndex, nullable(n.DisplayName),
				n.IsExternal, nullableID(n.RepositoryID), nullableID(w.nodeIDs[n.ContainerKey])); err != nil {
				return fmt.Errorf("inserting node %q: %w", n.Key, err)
			}
			// The id is always read back rather than taken from LastInsertId, because an
			// upsert that updates still reports a row affected while leaving LastInsertId
			// pointing at an unrelated row.
			var id int64
			if err := lookup.QueryRow(n.Key).Scan(&id); err != nil {
				return fmt.Errorf("resolving node %q: %w", n.Key, err)
			}
			w.nodeIDs[n.Key] = id
		}
		return nil
	}); err != nil {
		return err
	}
	return w.reloadAfterRevival(revived)
}

// reloadAfterRevival tells every open cursor to load the graph again when something removed came
// back: its row id is below their watermark, so no delta would ever carry it to a page that saw it go.
func (w *Writer) reloadAfterRevival(revived bool) error {
	if !revived {
		return nil
	}
	_, err := w.store.nextGeneration(true)
	return err
}

// anyRemoved reports whether any of keys names a tombstoned row of table.
//
// table and column are constants at every call site, never input.
func anyRemoved(tx *sql.Tx, table, column string, keys []string) (bool, error) {
	list, err := json.Marshal(keys)
	if err != nil {
		return false, err
	}
	var removed bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM `+table+` WHERE removed_at IS NOT NULL
		AND `+column+` IN (SELECT value FROM json_each(?)))`, string(list)).Scan(&removed); err != nil {
		return false, fmt.Errorf("reading what %s would bring back: %w", table, err)
	}
	return removed, nil
}

func (w *Writer) flushEdges() error {
	if len(w.edges) == 0 {
		return nil
	}
	pending := w.edges
	w.edges = nil
	broadened, revived := false, false
	keys := make([]string, len(pending))
	for i, e := range pending {
		keys[i] = e.Key
	}

	if err := w.inTx(func(tx *sql.Tx) error {
		var err error
		if revived, err = anyRemoved(tx, "abom_edge", "edge_key", keys); err != nil {
			return err
		}
		stmt, err := tx.Prepare(`
			INSERT INTO abom_edge
				(edge_key, query_run_id, src_node_row_id, dst_node_row_id, edge_kind,
				 ref_raw, ref_value, pin_comment, file_path, start_line, start_column,
				 end_line, end_column, repository_row_id, provenance)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(edge_key) DO UPDATE SET
				query_run_id    = CASE
					WHEN (SELECT analysis_mode FROM abom_query_run
					      WHERE row_id = excluded.query_run_id) = 'extended'
					 AND COALESCE((SELECT analysis_mode FROM abom_query_run
					               WHERE row_id = abom_edge.query_run_id), 'regular') = 'regular'
					THEN abom_edge.query_run_id ELSE excluded.query_run_id END,
				ref_raw         = excluded.ref_raw,
				ref_value       = excluded.ref_value,
				pin_comment     = excluded.pin_comment,
				dst_node_row_id = excluded.dst_node_row_id,
				-- ClassifyEdgeBoundaries only fills what is unset, so an edge whose target
				-- moved would keep the class of a target it no longer points at.
				boundary        = CASE WHEN abom_edge.dst_node_row_id = excluded.dst_node_row_id
				                       THEN abom_edge.boundary END,
				-- The same for every fact derived from where the edge points. They return as
				-- unknown rather than as a guess, and the next aggregate pass fills them again.
				-- owned_depth is not here: it measures the source side, which a repoint leaves
				-- alone.
				-- ref_style reads the reference as well as the target, so a line rewritten from
				-- @v4 to a commit on the same action has to forget it: keeping the old answer
				-- would report a tag until some later aggregate rebuild.
				ref_style       = CASE WHEN abom_edge.dst_node_row_id = excluded.dst_node_row_id
				                        AND abom_edge.ref_value IS excluded.ref_value
				                       THEN abom_edge.ref_style END,
				advisory_route  = CASE WHEN abom_edge.dst_node_row_id = excluded.dst_node_row_id
				                       THEN abom_edge.advisory_route END,
				finding_route   = CASE WHEN abom_edge.dst_node_row_id = excluded.dst_node_row_id
				                       THEN abom_edge.finding_route END,
				removed_at      = NULL,
				removed_generation = NULL`)
		if err != nil {
			return fmt.Errorf("preparing edge insert: %w", err)
		}
		defer stmt.Close()

		for _, e := range pending {
			srcID, ok := w.nodeIDs[e.SrcKey]
			if !ok {
				return fmt.Errorf("edge %q references unknown source node %q", e.Key, e.SrcKey)
			}
			dstID, ok := w.nodeIDs[e.DstKey]
			if !ok {
				return fmt.Errorf("edge %q references unknown target node %q", e.Key, e.DstKey)
			}
			gained, err := writeEdge(tx, stmt, e, srcID, dstID)
			if err != nil {
				return err
			}
			broadened = broadened || gained
		}
		// Endpoints too: a node the writer already knew skips its upsert, and a live edge under a
		// removed node is the inconsistency this exists to prevent.
		named, err := reviveNamedBy(tx, keys)
		revived = revived || named > 0
		return err
	}); err != nil {
		return err
	}
	if revived && !broadened {
		return w.reloadAfterRevival(true)
	}
	// A shared observation changes no row id, so the delta cannot describe it and every open
	// cursor is told to load the graph again. Only on a broadening: an ordinary crawl re-observes
	// what it already saw, and resetting for that would cost the whole payload every cycle.
	if broadened {
		if _, err := w.store.nextGeneration(true); err != nil {
			return err
		}
	}
	return nil
}

func writeEdge(tx *sql.Tx, statement *sql.Stmt, edge Edge, sourceID, targetID int64) (bool, error) {
	if err := guardEdgeAnalysisMode(tx, edge, targetID); err != nil {
		return false, err
	}
	broadens, err := edgeModeBroadens(tx, edge)
	if err != nil {
		return false, err
	}
	if _, err := statement.Exec(edge.Key, nullableID(edge.QueryRunID), sourceID, targetID, edge.Kind,
		nullable(edge.RefRaw), nullable(edge.RefValue), nullable(edge.PinComment),
		nullable(edge.FilePath), edge.StartLine, edge.StartColumn, edge.EndLine, edge.EndColumn,
		nullableID(edge.RepositoryID), nullable(edge.Provenance)); err != nil {
		return false, fmt.Errorf("inserting edge %q: %w", edge.Key, err)
	}
	return broadens, recordEdgeSighting(tx, edge, targetID)
}

func (w *Writer) inTx(fn func(*sql.Tx) error) error {
	tx, err := w.store.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Repository origins.
const (
	// OriginSeed marks a repository under review.
	OriginSeed = "seed"
	// OriginUpstream marks a repository fetched only to resolve what the seeds depend on.
	OriginUpstream = "upstream"
)

// OwnedOrigin is what the origins term calls the declaration's answer. No repository holds it as its
// origin: how a repository arrived never says whose it is.
const OwnedOrigin = "owned"

// UpsertRepository returns the row id for a repository, inserting it when absent.
func (s *Store) UpsertRepository(fullName, url string) (int64, error) {
	if _, err := s.db.Exec(
		`INSERT INTO repository (repository_full_name, repository_url) VALUES (?, ?)
		 ON CONFLICT(repository_full_name) DO UPDATE SET repository_url = excluded.repository_url`,
		fullName, nullable(url)); err != nil {
		return 0, fmt.Errorf("upserting repository %q: %w", fullName, err)
	}
	var id int64
	if err := s.db.QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = ?`, fullName).Scan(&id); err != nil {
		return 0, fmt.Errorf("resolving repository %q: %w", fullName, err)
	}
	return id, nil
}

// InsertUpstreamRepository adds a repository that is not under review.
//
// An existing row is left untouched: a repository can be both under review and depended on by
// others, and demoting it would erase it from its own report.
func (s *Store) InsertUpstreamRepository(fullName string) (int64, error) {
	if _, err := s.db.Exec(
		`INSERT INTO repository (repository_full_name, origin) VALUES (?, ?)
		 ON CONFLICT(repository_full_name) DO NOTHING`,
		fullName, OriginUpstream); err != nil {
		return 0, fmt.Errorf("inserting upstream repository %q: %w", fullName, err)
	}
	var id int64
	if err := s.db.QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = ?`, fullName).Scan(&id); err != nil {
		return 0, fmt.Errorf("resolving repository %q: %w", fullName, err)
	}
	return id, nil
}

// InsertOwnedRepository records a repository the operator owns.
//
// It promotes an existing row rather than skipping it: a repository first seen as somebody else's
// dependency is recorded `upstream`, and a declaration is new information about it — the operator
// owns it, so it is a root of its own report and not only a leaf of someone else's. This is the
// one direction that changes an origin; InsertUpstreamRepository never demotes, so the two cannot
// fight over the same row.
func (s *Store) InsertOwnedRepository(fullName string) (int64, error) {
	if _, err := s.db.Exec(
		`INSERT INTO repository (repository_full_name, origin) VALUES (?, ?)
		 ON CONFLICT(repository_full_name) DO UPDATE SET origin = excluded.origin`,
		fullName, OriginSeed); err != nil {
		return 0, fmt.Errorf("inserting owned repository %q: %w", fullName, err)
	}
	var id int64
	if err := s.db.QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = ?`, fullName).Scan(&id); err != nil {
		return 0, fmt.Errorf("resolving repository %q: %w", fullName, err)
	}
	return id, nil
}

// RepositoryOrigin reports how a repository is currently recorded, or "" if the corpus has never
// seen it. The distinction is what tells a caller whether it found something new or re-found
// something it already had under another description.
func (s *Store) RepositoryOrigin(fullName string) (string, error) {
	var origin string
	err := s.db.QueryRow(
		`SELECT origin FROM repository WHERE repository_full_name = ?`, fullName).Scan(&origin)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading origin of %q: %w", fullName, err)
	}
	return origin, nil
}

// nullable stores empty strings as NULL so that "absent" and "empty" stay distinguishable and
// indexes do not fill with meaningless empty keys.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
