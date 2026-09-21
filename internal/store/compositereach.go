package store

import "fmt"

// maxCompositeDepth bounds the walk. Composite actions nest, and a ring of them is a real shape
// rather than corruption, so the traversal terminates by depth instead of assuming a tree.
const maxCompositeDepth = 8

// ReachedAction is an action that runs because of a `uses:` that does not name it.
//
// GitHub's allow-list is checked against `Through`, the reference the workflow wrote. Nothing
// checks `Reached`. Only the closure ABOM holds can put the two side by side.
type ReachedAction struct {
	// Repository, FilePath and StartLine locate the `uses:` the operator can actually change.
	Repository string
	FilePath   string
	StartLine  int
	// Through is the action the workflow named; Reached is what it goes on to run.
	Through string
	Reached string
	// The refs as they were written. An allow-list pattern is matched against `owner/repo@ref`,
	// so a decision made without these compares a different string than GitHub would.
	ThroughRef string
	ReachedRef string
	Depth      int
}

// ActionsReachedThroughComposites walks from every top-level `uses:` into the composite actions it
// lands on, and reports what runs that the workflow never named.
//
// The closure is computed over actions and only then joined to call sites. Carrying the call site
// through the recursion instead re-walks the same subtree once per caller, which on the reference
// corpus is 4,970 callers over 201 composites and did not finish in five and a half minutes.
func (s *Store) ActionsReachedThroughComposites() ([]ReachedAction, error) {
	rows, err := s.db.Query(`
		WITH RECURSIVE
		-- The composite a caller reaches is the one its subpath names, so a holder is a repository
		-- *and* a directory. Keying on the repository alone had a plain github/codeql-action
		-- reference reaching an action named only by that repository's own CI helper, which no
		-- consumer runs.
		inner_step AS (
			SELECT lower(r.repository_full_name) AS holder_repo,
			       lower(COALESCE(src.path, '')) AS holder_path,
			       lower(dst.node_key) AS reached,
			       COALESCE(e.ref_value, '') AS ref
			FROM abom_edge_regular e
			JOIN abom_node_live src ON src.row_id = e.src_node_row_id
			JOIN abom_node_live dst ON dst.row_id = e.dst_node_row_id
			JOIN repository r ON r.row_id = src.repository_row_id
			WHERE e.edge_kind = 'composite-step-uses-action'
			  AND dst.owner <> '' AND dst.repo <> ''
		),
		-- owner/repo/dir for a composite in a subdirectory, owner/repo for one at the root:
		-- exactly the reference a caller writes to reach it.
		holder AS (
			SELECT DISTINCT
				CASE
					WHEN holder_path IN ('action.yml', 'action.yaml') THEN holder_repo
					ELSE holder_repo || '/' ||
					     rtrim(rtrim(holder_path, 'action.yml'), 'action.yaml')
				END AS root,
				reached, ref
			FROM inner_step
			WHERE holder_path LIKE '%action.yml' OR holder_path LIKE '%action.yaml'
		),
		walk(root, reached, ref, depth, seen) AS (
			SELECT rtrim(root, '/'), reached, ref, 1,
			       '/' || rtrim(root, '/') || '/' || reached || '/'
			FROM holder
			UNION ALL
			SELECT w.root, h.reached, h.ref, w.depth + 1, w.seen || h.reached || '/'
			FROM walk w
			JOIN holder h ON rtrim(h.root, '/') = w.reached
			WHERE w.depth < ?
			  AND instr(w.seen, '/' || h.reached || '/') = 0
		),
		reach AS (
			SELECT root, reached, ref, MIN(depth) AS depth
			FROM walk GROUP BY root, reached, ref
		),
		top_level AS (
			SELECT lower(r.repository_full_name) AS repository,
			       COALESCE(e.file_path, '') AS file_path,
			       COALESCE(e.start_line, 0) AS start_line,
			       lower(dst.node_key) AS through,
			       COALESCE(e.ref_value, '') AS ref
			FROM abom_edge_regular e
			JOIN abom_node_live dst ON dst.row_id = e.dst_node_row_id
			JOIN repository r ON r.row_id = e.repository_row_id
			WHERE e.edge_kind = 'step-uses-action'
			  AND dst.owner <> '' AND dst.repo <> ''
		)
		SELECT DISTINCT t.repository, t.file_path, t.start_line, t.through, t.ref,
		       r.reached, r.ref, r.depth
		FROM top_level t
		JOIN reach r ON r.root = t.through
		-- An action the workflow also names directly is GitHub's to police, not ours.
		WHERE r.reached <> t.through
		ORDER BY t.repository, t.file_path, t.start_line, r.reached`, maxCompositeDepth)
	if err != nil {
		return nil, fmt.Errorf("walking composite references: %w", err)
	}
	defer rows.Close()

	var out []ReachedAction
	for rows.Next() {
		var r ReachedAction
		if err := rows.Scan(&r.Repository, &r.FilePath, &r.StartLine, &r.Through, &r.ThroughRef,
			&r.Reached, &r.ReachedRef, &r.Depth); err != nil {
			return nil, fmt.Errorf("scanning a reached action: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
