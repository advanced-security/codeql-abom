package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// LiveNode and LiveEdge are the predicates the views apply. Reads never spell them: they name
// `abom_node_live` and `abom_edge_live` instead, so what "live" means is defined once and a read
// cannot opt out of it by forgetting a clause.
//
// Tombstones keep row ids monotonic for delta cursors and let a delta name what disappeared.
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
	// Queued is frontier rows for repositories the closure does not reach. Removing the graph and
	// leaving these behind does not settle: the crawl claims them, ingestion is not closure-gated,
	// and their nodes come straight back.
	Queued int `json:"queued"`
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

// ErrEmptyClosure is pruning refusing to act on a corpus nothing has been enumerated into.
//
// Named rather than described, because a read asking what a prune would cost has to tell it apart
// from a real failure: it is the state every install passes through between declaring an account
// and the first enumeration finishing.
var ErrEmptyClosure = errors.New(
	"store: refusing to prune against an empty closure; declare an owned scope first")

// ErrUnanalyzedClosure is pruning refusing to act on a closure nothing has been analyzed into.
//
// A declared account that has only been enumerated has repositories and no edges, so it reaches
// nothing and the whole corpus falls outside it. That is not a measurement that the corpus is
// unreachable; it is the absence of one, and acting on it removes everything the closure has not
// had the chance to reach yet.
var ErrUnanalyzedClosure = errors.New(
	"store: refusing to prune against a closure nothing has been analyzed into; " +
		"what it reaches is unknown until analysis has run, and pruning now would remove " +
		"everything it is going to reach")

// closureHasEvidence reports whether anything in the closure has produced an edge.
//
// The edge is the evidence. Without one the reach of a declared repository is unknown, and every
// count taken against it describes a corpus nothing has looked at rather than one found wanting.
func (s *Store) closureHasEvidence(inClosure []string) (bool, error) {
	list, args := inList(inClosure)
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_edge e
		  JOIN repository r ON r.row_id = e.repository_row_id
		 WHERE e.removed_at IS NULL
		   AND LOWER(r.repository_full_name) IN `+list, args...).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("reading whether the closure has been analyzed: %w", err)
	}
	return n > 0, nil
}

// outsideClosure builds the tests for what the declared closure does not reach.
//
// Shared by the count, the marking and the naming so the three cannot drift apart: a list that
// disagreed with the number printed beside it would be read as one of them being wrong about the
// corpus rather than about itself.
func outsideClosure(inClosure []string) (deadEdges, deadNodes, deadQueueRows string, args []any) {
	list, args := inList(inClosure)
	// An edge belongs to the repository that contains it, so containment decides admission and
	// the endpoints follow. A node is live while any live edge names it, or while it belongs to
	// a repository inside the closure.
	deadEdges = `abom_edge.row_id IN (
		SELECT e.row_id FROM abom_edge e
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE LOWER(r.repository_full_name) NOT IN ` + list + `)`
	deadNodes = `abom_node.row_id IN (
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
	// A queue row names the repository it will fetch, so the same boundary decides it. Rows with no
	// repository are left alone: they name nothing the closure can rule on, and guessing would
	// delete work over a blank field.
	deadQueueRows = `owner <> '' AND repo <> ''
	                  AND LOWER(owner || '/' || repo) NOT IN ` + list
	return deadEdges, deadNodes, deadQueueRows, args
}

// RepositoriesOutsideClosure names the repositories a prune would take something from.
//
// The count answers how big a prune is; this answers which working copies on disk it makes dead
// weight, which is a question only a name can settle.
func (s *Store) RepositoriesOutsideClosure(inClosure []string) ([]string, error) {
	if len(inClosure) == 0 {
		return nil, ErrEmptyClosure
	}
	if ok, err := s.closureHasEvidence(inClosure); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrUnanalyzedClosure
	}
	deadEdges, deadNodes, _, args := outsideClosure(inClosure)
	rows, err := s.db.Query(`
		SELECT DISTINCT name FROM (
		    SELECT r.repository_full_name AS name FROM abom_node
		      JOIN repository r ON r.row_id = abom_node.repository_row_id
		     WHERE `+LiveNode+` AND `+deadNodes+`
		    UNION
		    SELECT r.repository_full_name AS name FROM abom_edge
		      JOIN repository r ON r.row_id = abom_edge.repository_row_id
		     WHERE `+LiveEdge+` AND `+deadEdges+`)
		 ORDER BY name`, repeated(args, 4)...)
	if err != nil {
		return nil, fmt.Errorf("naming what is outside the closure: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (s *Store) pruneOutsideClosure(inClosure []string, act bool) (PruneReport, error) {
	var out PruneReport
	// An undeclared corpus produces an empty closure, and pruning everything on the strength of
	// a configuration nobody has written yet is the most destructive thing this could do.
	if len(inClosure) == 0 {
		return out, ErrEmptyClosure
	}
	// A closure with no edges behind it has never been analyzed, and everything outside it is
	// everything.
	if ok, err := s.closureHasEvidence(inClosure); err != nil {
		return out, err
	} else if !ok {
		return out, ErrUnanalyzedClosure
	}

	deadEdges, deadNodes, deadQueueRows, args := outsideClosure(inClosure)

	counts := []struct {
		into  *int
		query string
		// lists is how many times the closure list appears, since each occurrence needs its own
		// copy of the arguments.
		lists int
	}{
		{&out.Edges, `SELECT COUNT(*) FROM abom_edge WHERE ` + LiveEdge + ` AND ` + deadEdges, 1},
		{&out.Nodes, `SELECT COUNT(*) FROM abom_node WHERE ` + LiveNode + ` AND ` + deadNodes, 3},
		// Repositories that lose something, not repositories that merely sit outside the closure.
		// The looser count reports the same number after a prune as before it, and an outside
		// repository whose nodes are all held live by an edge from inside loses nothing at all.
		{&out.Repositories, `SELECT COUNT(*) FROM (
		        SELECT r.row_id FROM abom_node
		          JOIN repository r ON r.row_id = abom_node.repository_row_id
		         WHERE ` + LiveNode + ` AND ` + deadNodes + `
		        UNION
		        SELECT r.row_id FROM abom_edge
		          JOIN repository r ON r.row_id = abom_edge.repository_row_id
		         WHERE ` + LiveEdge + ` AND ` + deadEdges + `)`, 4},
		{&out.Queued, `SELECT COUNT(*) FROM abom_frontier WHERE ` + deadQueueRows, 1},
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

	// The queue goes with the graph, and it goes now rather than at compaction: a crawl between
	// the two would claim these rows, analyze them, and write their nodes straight back, because
	// only onward discovery is closure-gated and ingestion is not. Deleted rather than tombstoned
	// because the frontier is a work queue, not part of the append-only graph — and the ledger
	// still records that the work happened, so re-declaring the account does not redo it.
	if _, err := s.db.Exec(
		`DELETE FROM abom_frontier WHERE `+deadQueueRows, args...); err != nil {
		return PruneReport{}, fmt.Errorf("removing queued work outside the closure: %w", err)
	}
	return out, nil
}

// nextGeneration records a generation for a change that is not an ingestion, so a client can be
// told about it through the same cursor it already polls.
func (s *Store) nextGeneration(bumpEpoch bool) (int64, error) {
	return s.recordGraphGeneration(GraphRebuild{}, bumpEpoch, false)
}

// reviveNamedBy brings back every removed node that one of the given live edges names, or that any
// live edge names when keys is nil, and reports how many came back. A live edge under a removed
// node says something exists that the graph says does not.
func reviveNamedBy(db interface {
	Exec(string, ...any) (sql.Result, error)
}, keys []string) (int64, error) {
	scope, args := "", []any{}
	if keys != nil {
		list, err := json.Marshal(keys)
		if err != nil {
			return 0, err
		}
		scope, args = ` AND edge_key IN (SELECT value FROM json_each(?))`, []any{string(list), string(list)}
	}
	res, err := db.Exec(`
		UPDATE abom_node SET removed_at = NULL, removed_generation = NULL
		 WHERE removed_at IS NOT NULL AND row_id IN (
		       SELECT src_node_row_id FROM abom_edge WHERE removed_at IS NULL`+scope+`
		       UNION
		       SELECT dst_node_row_id FROM abom_edge WHERE removed_at IS NULL`+scope+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("bringing back what live edges name: %w", err)
	}
	return res.RowsAffected()
}

// reviveWhatLiveEdgesName repairs a database written before a run naming a pruned node brought it
// back, and tells open cursors to reload if anything did.
func (s *Store) reviveWhatLiveEdgesName() error {
	n, err := reviveNamedBy(s.db, nil)
	if err != nil || n == 0 {
		return err
	}
	_, err = s.nextGeneration(true)
	return err
}

// removedSince names what was tombstoned after a generation, so a client hears about a removal
// exactly once rather than on every poll for the rest of the session.
//
// The table is a constant at every call site, never user input.
func (s *Store) removedSince(table string, since int64) ([]string, error) {
	key, kind := "node_key", "node"
	if table == "abom_edge" {
		key, kind = "edge_key", "edge"
	}
	return queryAll(s, "reading removals", func(r *sql.Rows) (string, error) {
		var k string
		return k, r.Scan(&k)
	}, `SELECT `+key+` FROM `+table+` WHERE removed_at IS NOT NULL
		AND row_id IN (SELECT object_id FROM abom_graph_change WHERE object_kind = ? AND sequence > ?)
		ORDER BY row_id`, kind, since)
}

// Tombstoned counts nodes removed from view and still on disk.
//
// Reads the raw table on purpose: a tombstone is precisely what the live views hide, so asking
// them would always answer zero.
func (s *Store) Tombstoned() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_node WHERE removed_at IS NOT NULL`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting tombstoned nodes: %w", err)
	}
	return n, nil
}

// LiveSize is how much of the graph is in view.
type LiveSize struct {
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
}

// TakesMostOf reports a removal of at least half of what is in view, by nodes or by edges. Applied
// only when said outright: a scope that could not be confirmed — a listing that under-reported, an
// organization a `gh` check could not see — reads exactly like one that owns little, and both put
// most of the graph outside it.
func (r PruneReport) TakesMostOf(live LiveSize) bool {
	return (live.Nodes > 0 && r.Nodes*2 >= live.Nodes) || (live.Edges > 0 && r.Edges*2 >= live.Edges)
}

// LiveGraph counts what is in view, so a removal can be read as a share of it.
func (s *Store) LiveGraph() (LiveSize, error) {
	var out LiveSize
	err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM abom_node_live), (SELECT COUNT(*) FROM abom_edge_live)`).
		Scan(&out.Nodes, &out.Edges)
	if err != nil {
		return LiveSize{}, fmt.Errorf("counting the live graph: %w", err)
	}
	return out, nil
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
	// findings link nodes, and edges name them as endpoints rather than by repository.
	for _, stmt := range []string{
		`DELETE FROM abom_finding_node WHERE node_row_id IN
		   (SELECT row_id FROM abom_node WHERE removed_at IS NOT NULL)`,
		`DELETE FROM abom_surface WHERE node_row_id IN
		   (SELECT row_id FROM abom_node WHERE removed_at IS NOT NULL)`,
		`DELETE FROM abom_blast_radius WHERE node_row_id IN
		   (SELECT row_id FROM abom_node WHERE removed_at IS NOT NULL)`,
		`DELETE FROM abom_node_reach WHERE node_row_id IN
		   (SELECT row_id FROM abom_node WHERE removed_at IS NOT NULL)`,
		`DELETE FROM abom_node_version_reach WHERE node_row_id IN
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
	if len(names) == 0 {
		return "(SELECT CAST(NULL AS TEXT) WHERE false)", []any{}
	}
	args := make([]any, 0, len(names))
	marks := make([]string, 0, len(names))
	for _, n := range names {
		args = append(args, strings.ToLower(n))
		marks = append(marks, "?")
	}
	return "(" + strings.Join(marks, ", ") + ")", args
}
