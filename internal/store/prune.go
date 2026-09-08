package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// LiveNode and LiveEdge are the predicates the views apply. Reads never spell them: they name
// `abom_node_live` and `abom_edge_live` instead, so what "live" means is defined once and a read
// cannot opt out of it by forgetting a clause.
//
// A tombstone rather than a DELETE because every delta cursor the interface holds is a row-id
// watermark: removing rows would silently hand a client an update that skips or repeats work.
// Marking keeps row ids monotonic and lets the delta *say* what disappeared, which is what makes
// removal expressible at all.
const (
	LiveNode = `abom_node.removed_at IS NULL`
	LiveEdge = `abom_edge.removed_at IS NULL`
)

// liveViewDDL is applied by Migrate, after the tombstone columns exist.
//
// Views rather than a rename: SQLite's ALTER TABLE ... RENAME TO does not rewrite foreign key
// references, so swapping the tables out from under the six that reference `abom_node` leaves
// them pointing at a view and `PRAGMA foreign_key_check` fails. A view beside the table costs
// nothing — SQLite flattens it, and the query plans are identical.
const liveViewDDL = `
CREATE VIEW IF NOT EXISTS abom_node_live AS SELECT * FROM abom_node WHERE removed_at IS NULL;
CREATE VIEW IF NOT EXISTS abom_edge_live AS SELECT * FROM abom_edge WHERE removed_at IS NULL;`

// PruneReport is what a prune did, or would do.
type PruneReport struct {
	// Repositories counts repositories entirely outside the closure, which is the number an
	// operator recognizes. Nodes and Edges are what actually moves.
	Repositories int `json:"repositories"`
	Nodes        int `json:"nodes"`
	Edges        int `json:"edges"`
}

// ExplainOutsideClosure reports what a prune would remove, without removing it.
//
// A destructive operation whose scope is only visible afterwards is one nobody will run twice.
func (s *Store) ExplainOutsideClosure(inClosure []string) (PruneReport, error) {
	return s.pruneOutsideClosure(inClosure, false)
}

// MarkOutsideClosure tombstones everything the declared scope does not reach.
func (s *Store) MarkOutsideClosure(inClosure []string) (PruneReport, error) {
	return s.pruneOutsideClosure(inClosure, true)
}

func (s *Store) pruneOutsideClosure(inClosure []string, act bool) (PruneReport, error) {
	var out PruneReport
	// An undeclared corpus produces an empty closure, and pruning everything on the strength of
	// a configuration nobody has written yet is the most destructive thing this could do.
	if len(inClosure) == 0 {
		return out, fmt.Errorf(
			"store: refusing to prune against an empty closure; declare an owned scope first")
	}

	list, args := inList(inClosure)
	// An edge belongs to the repository that contains it, so containment decides admission and
	// the endpoints follow. A node is live while any live edge names it, or while it belongs to
	// a repository inside the closure.
	deadEdges := `abom_edge.row_id IN (
		SELECT e.row_id FROM abom_edge e
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE LOWER(r.repository_full_name) NOT IN ` + list + `)`
	deadNodes := `abom_node.row_id IN (
		SELECT n.row_id FROM abom_node n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		WHERE (r.repository_full_name IS NULL
		       OR LOWER(r.repository_full_name) NOT IN ` + list + `)
		  AND (n.owner IS NULL OR n.repo IS NULL
		       OR LOWER(n.owner || '/' || n.repo) NOT IN ` + list + `)
		  AND NOT EXISTS (
		        SELECT 1 FROM abom_edge e
		         JOIN repository er ON er.row_id = e.repository_row_id
		         WHERE (e.src_node_row_id = n.row_id OR e.dst_node_row_id = n.row_id)
		           AND e.removed_at IS NULL
		           AND LOWER(er.repository_full_name) IN ` + list + `))`

	counts := []struct {
		into  *int
		query string
		// lists is how many times the closure list appears, since each occurrence needs its own
		// copy of the arguments.
		lists int
	}{
		{&out.Edges, `SELECT COUNT(*) FROM abom_edge WHERE ` + LiveEdge + ` AND ` + deadEdges, 1},
		{&out.Nodes, `SELECT COUNT(*) FROM abom_node WHERE ` + LiveNode + ` AND ` + deadNodes, 3},
		{&out.Repositories, `SELECT COUNT(*) FROM repository
		                      WHERE LOWER(repository_full_name) NOT IN ` + list, 1},
	}
	for _, c := range counts {
		if err := s.db.QueryRow(c.query, repeated(args, c.lists)...).Scan(c.into); err != nil {
			return PruneReport{}, fmt.Errorf("counting what is outside the closure: %w", err)
		}
	}
	if !act {
		return out, nil
	}

	at := now()
	// The generation a client's cursor is expressed in, so a removal can be reported exactly
	// once: a delta since G carries what was removed after G and nothing it has already seen.
	gen, err := s.nextGeneration(false)
	if err != nil {
		return PruneReport{}, err
	}

	// Edges first: a node is live while a live edge names it, so marking nodes before edges
	// would read the old edge state and keep endpoints nothing reaches any more.
	updates := []struct {
		stmt  string
		lists int
	}{
		{`UPDATE abom_edge SET removed_at = ?, removed_generation = ?
		   WHERE ` + LiveEdge + ` AND ` + deadEdges, 1},
		{`UPDATE abom_node SET removed_at = ?, removed_generation = ?
		   WHERE ` + LiveNode + ` AND ` + deadNodes, 3},
	}
	for _, u := range updates {
		argv := append([]any{at, gen}, repeated(args, u.lists)...)
		if _, err := s.db.Exec(u.stmt, argv...); err != nil {
			return PruneReport{}, fmt.Errorf("marking what is outside the closure: %w", err)
		}
	}
	return out, nil
}

// nextGeneration records a generation for a change that is not an ingestion, so a client can be
// told about it through the same cursor it already polls.
func (s *Store) nextGeneration(bumpEpoch bool) (int64, error) {
	var nodeMark, edgeMark sql.NullInt64
	if err := s.db.QueryRow(
		`SELECT MAX(row_id) FROM abom_node`).Scan(&nodeMark); err != nil {
		return 0, fmt.Errorf("reading the node watermark: %w", err)
	}
	if err := s.db.QueryRow(
		`SELECT MAX(row_id) FROM abom_edge`).Scan(&edgeMark); err != nil {
		return 0, fmt.Errorf("reading the edge watermark: %w", err)
	}
	var epoch int64
	if err := s.db.QueryRow(
		`SELECT COALESCE(MAX(epoch), 0) FROM abom_graph_generation`).Scan(&epoch); err != nil {
		return 0, fmt.Errorf("reading the epoch: %w", err)
	}
	// A new row rather than an update, so a cursor pointing at the previous generation still
	// reads the previous epoch and can be told its row ids no longer mean anything.
	if bumpEpoch {
		epoch++
	}
	res, err := s.db.Exec(`
		INSERT INTO abom_graph_generation
			(node_watermark, edge_watermark, created_at, reach_rebuilt, epoch)
		VALUES (?, ?, ?, 0, ?)`, nodeMark.Int64, edgeMark.Int64, now(), epoch)
	if err != nil {
		return 0, fmt.Errorf("recording a generation: %w", err)
	}
	return res.LastInsertId()
}

// removedSince names what was tombstoned after a generation, so a client hears about a removal
// exactly once rather than on every poll for the rest of the session.
//
// The table is a constant at every call site, never user input.
func (s *Store) removedSince(table string, since int64) ([]string, error) {
	key := "node_key"
	if table == "abom_edge" {
		key = "edge_key"
	}
	return queryAll(s, "reading removals", func(r *sql.Rows) (string, error) {
		var k string
		return k, r.Scan(&k)
	}, `SELECT `+key+` FROM `+table+` WHERE removed_generation > ? ORDER BY row_id`, since)
}

// Compact deletes what has been tombstoned and ends the meaning of every row-id cursor.
//
// Separate from marking so a prune stays reversible right up until somebody decides it is not,
// and because this is the step that makes a client reload rather than receive a delta that skips
// rows it never saw arrive.
func (s *Store) Compact() (PruneReport, error) {
	var out PruneReport
	for _, c := range []struct {
		into  *int
		query string
	}{
		{&out.Edges, `SELECT COUNT(*) FROM abom_edge WHERE removed_at IS NOT NULL`},
		{&out.Nodes, `SELECT COUNT(*) FROM abom_node WHERE removed_at IS NOT NULL`},
	} {
		if err := s.db.QueryRow(c.query).Scan(c.into); err != nil {
			return PruneReport{}, fmt.Errorf("counting tombstones: %w", err)
		}
	}
	// A no-op must not bump the epoch: every open page would reload for nothing.
	if out.Nodes == 0 && out.Edges == 0 {
		return out, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return PruneReport{}, fmt.Errorf("compacting: %w", err)
	}
	defer tx.Rollback()

	// The closure that refused a naive delete when the junk `/` repository was removed by hand:
	// findings and alerts link nodes, and edges name them as endpoints rather than by repository.
	for _, stmt := range []string{
		`DELETE FROM abom_finding_node WHERE node_row_id IN
		   (SELECT row_id FROM abom_node WHERE removed_at IS NOT NULL)`,
		`DELETE FROM abom_alert_link WHERE node_row_id IN
		   (SELECT row_id FROM abom_node WHERE removed_at IS NOT NULL)`,
		`DELETE FROM abom_surface WHERE node_row_id IN
		   (SELECT row_id FROM abom_node WHERE removed_at IS NOT NULL)`,
		`DELETE FROM abom_blast_radius WHERE node_row_id IN
		   (SELECT row_id FROM abom_node WHERE removed_at IS NOT NULL)`,
		`DELETE FROM abom_exposure WHERE node_row_id IN
		   (SELECT row_id FROM abom_node WHERE removed_at IS NOT NULL)`,
		`DELETE FROM abom_edge WHERE removed_at IS NOT NULL
		    OR src_node_row_id IN (SELECT row_id FROM abom_node WHERE removed_at IS NOT NULL)
		    OR dst_node_row_id IN (SELECT row_id FROM abom_node WHERE removed_at IS NOT NULL)`,
		// Containment is a self-reference, so a surviving child would point at a deleted parent.
		`UPDATE abom_node SET container_node_row_id = NULL WHERE container_node_row_id IN
		   (SELECT row_id FROM abom_node WHERE removed_at IS NOT NULL)`,
		`DELETE FROM abom_node WHERE removed_at IS NOT NULL`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return PruneReport{}, fmt.Errorf("compacting: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return PruneReport{}, fmt.Errorf("compacting: %w", err)
	}
	if _, err := s.nextGeneration(true); err != nil {
		return PruneReport{}, err
	}
	return out, nil
}

// repeated copies the argument list once per occurrence of the IN list in a statement.
func repeated(args []any, times int) []any {
	out := make([]any, 0, len(args)*times)
	for range times {
		out = append(out, args...)
	}
	return out
}

// inList renders a lower-cased SQL IN list, because GitHub treats account names
// case-insensitively and so must this.
func inList(names []string) (string, []any) {
	args := make([]any, 0, len(names))
	marks := make([]string, 0, len(names))
	for _, n := range names {
		args = append(args, strings.ToLower(n))
		marks = append(marks, "?")
	}
	return "(" + strings.Join(marks, ", ") + ")", args
}
