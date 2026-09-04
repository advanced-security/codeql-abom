package store

import (
	"database/sql"
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

	return w.inTx(func(tx *sql.Tx) error {
		insert, err := tx.Prepare(`
			INSERT INTO abom_node
				(node_key, node_kind, owner, repo, path, job_id, step_index,
				 display_name, is_external, repository_row_id, container_node_row_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(node_key) DO UPDATE SET
				container_node_row_id = COALESCE(excluded.container_node_row_id,
				                                 abom_node.container_node_row_id)`)
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
	})
}

func (w *Writer) flushEdges() error {
	if len(w.edges) == 0 {
		return nil
	}
	pending := w.edges
	w.edges = nil

	return w.inTx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`
			INSERT INTO abom_edge
				(edge_key, query_run_id, src_node_row_id, dst_node_row_id, edge_kind,
				 ref_raw, ref_value, pin_comment, file_path, start_line, start_column,
				 end_line, end_column, repository_row_id, provenance)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(edge_key) DO UPDATE SET
				query_run_id    = excluded.query_run_id,
				ref_raw         = excluded.ref_raw,
				ref_value       = excluded.ref_value,
				pin_comment     = excluded.pin_comment,
				dst_node_row_id = excluded.dst_node_row_id`)
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
			if _, err := stmt.Exec(e.Key, nullableID(e.QueryRunID), srcID, dstID, e.Kind,
				nullable(e.RefRaw), nullable(e.RefValue), nullable(e.PinComment),
				nullable(e.FilePath), e.StartLine, e.StartColumn, e.EndLine, e.EndColumn,
				nullableID(e.RepositoryID), nullable(e.Provenance)); err != nil {
				return fmt.Errorf("inserting edge %q: %w", e.Key, err)
			}
		}
		return nil
	})
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
