package store

import (
	"fmt"
	"strings"
)

// LockSelection is a dependency a lock selects at a commit other than the ref its source
// declares: what the workflow runs, rather than what it says.
type LockSelection struct {
	NodeKey string
	Owner   string
	Repo    string
	Path    string
	// Commit is the lock's digest, and the ref the frontier checks out.
	Commit string
}

// LockSelectedDependencies reports what locks select for the external dependencies one analyzed
// repository names.
//
// Three ways a lock governs an edge, and only these three, as the pinned CLI writes them:
//
//   - A step in one of the repository's own workflows is governed by that repository's lock,
//     for that workflow only. The same pin locked for another workflow is that workflow's
//     answer, and borrowing it would describe code this one does not run.
//   - A step inside one of the repository's own composites (`./` or `$/`) is governed by the
//     lock of each workflow that reaches the composite, which lists the composite's pins as its
//     own. A workflow that never calls the composite has no say in it.
//   - A step inside a composite action somebody else calls is governed by the caller's lock.
//     The CLI records a remote composite's nested pins in its caller's DAG and never reads the
//     callee's lock, so the composite's own lock says nothing here.
//
// Only locks read at a commit the repository currently resolves to count: a lock the frontier
// has moved past describes code nobody runs. analyzed names the commit this call is settling,
// which the frontier has not recorded yet. A selection the declared ref already names is not
// reported, and only SHA-1 digests are, because nothing here can fetch another kind.
func (s *Store) LockSelectedDependencies(repository, analyzed string) ([]LockSelection, error) {
	owner, repo, ok := strings.Cut(repository, "/")
	if !ok {
		return []LockSelection{}, nil
	}
	// Asked first, by key, because almost nothing is locked: without it every settle would pay
	// for the frontier walk below on behalf of a lock that does not exist.
	var governed int
	if err := s.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM abom_lock_workflow_pin WHERE repository = ?1)
		    OR EXISTS(SELECT 1 FROM abom_lock_dependency WHERE owner = LOWER(?2) AND repo = LOWER(?3))`,
		repository, owner, repo).Scan(&governed); err != nil {
		return nil, fmt.Errorf("asking whether a lock governs %s: %w", repository, err)
	}
	if governed == 0 {
		return []LockSelection{}, nil
	}
	rows, err := s.db.Query(lockSelectionQuery, repository, owner, repo, analyzed)
	if err != nil {
		return nil, fmt.Errorf("reading lock selections for %s: %w", repository, err)
	}
	defer rows.Close()

	out := []LockSelection{}
	for rows.Next() {
		var sel LockSelection
		if err := rows.Scan(&sel.NodeKey, &sel.Owner, &sel.Repo, &sel.Path, &sel.Commit); err != nil {
			return nil, fmt.Errorf("reading lock selections for %s: %w", repository, err)
		}
		out = append(out, sel)
	}
	return out, rows.Err()
}

// lockSelectionQuery takes the repository, its owner, its name and the commit being settled.
// Every lock table is reached by its key, never scanned: the tables grow with every locked commit
// the corpus holds, and this runs once per repository settled.
const lockSelectionQuery = `
	WITH RECURSIVE current_root(repository, commit_sha) AS (
	  SELECT LOWER(?1), LOWER(?4) WHERE ?4 <> ''
	  UNION
	  SELECT LOWER(f.owner || '/' || f.repo), LOWER(f.resolved_sha) FROM abom_frontier f
	  WHERE f.owner IS NOT NULL AND f.repo IS NOT NULL
	    AND COALESCE(f.resolved_sha, '') <> ''
	),
	-- The repository's own composites each of its workflows reaches, through any depth of
	-- composites calling composites.
	reach(workflow_path, node_row_id) AS (
	  SELECT e.file_path, e.dst_node_row_id
	  FROM abom_edge_regular e
	  JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	  JOIN repository r ON r.row_id = e.repository_row_id
	  WHERE r.repository_full_name = ?1
	    AND e.edge_kind = 'step-uses-action' AND n.is_external = 0
	  UNION
	  -- CROSS JOIN fixes the order: outward from what is reached, never inward from every
	  -- internal node the corpus holds.
	  SELECT reach.workflow_path, e.dst_node_row_id
	  FROM reach
	  CROSS JOIN abom_node_live s ON s.container_node_row_id = reach.node_row_id
	  CROSS JOIN abom_edge_regular e ON e.src_node_row_id = s.row_id
	  CROSS JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	  WHERE e.edge_kind = 'composite-step-uses-action' AND n.is_external = 0
	)
	SELECT n.node_key, n.owner, n.repo, COALESCE(n.path, ''), d.digest
	FROM abom_edge_regular e
	JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	JOIN repository r ON r.row_id = e.repository_row_id
	JOIN abom_lock_workflow_pin w ON w.repository = ?1 AND w.workflow_path = e.file_path
	JOIN current_root c ON c.repository = LOWER(?1) AND c.commit_sha = LOWER(w.commit_sha)
	JOIN abom_lock_dependency d
	  ON d.repository = w.repository AND d.commit_sha = w.commit_sha AND d.pin_key = w.pin_key
	WHERE r.repository_full_name = ?1
	  AND e.edge_kind = 'step-uses-action'
	  AND n.is_external = 1 AND n.owner IS NOT NULL AND n.repo IS NOT NULL
	  AND d.owner = LOWER(n.owner) AND d.repo = LOWER(n.repo)
	  AND d.ref = COALESCE(e.ref_value, '')
	  AND d.digest_algorithm = 'sha1' AND d.digest <> ''
	  AND LOWER(COALESCE(e.ref_value, '')) <> d.digest
	UNION
	SELECT n.node_key, n.owner, n.repo, COALESCE(n.path, ''), child.digest
	FROM abom_edge_regular e
	JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	JOIN repository r ON r.row_id = e.repository_row_id
	JOIN abom_lock_dependency parent ON parent.owner = LOWER(?2) AND parent.repo = LOWER(?3)
	JOIN current_root c
	  ON c.repository = LOWER(parent.repository) AND c.commit_sha = LOWER(parent.commit_sha)
	JOIN abom_lock_use u
	  ON u.repository = parent.repository AND u.commit_sha = parent.commit_sha
	 AND u.parent_pin = parent.pin_key
	JOIN abom_lock_dependency child
	  ON child.repository = u.repository AND child.commit_sha = u.commit_sha
	 AND child.pin_key = u.child_pin
	WHERE r.repository_full_name = ?1
	  AND e.edge_kind = 'composite-step-uses-action'
	  AND n.is_external = 1 AND n.owner IS NOT NULL AND n.repo IS NOT NULL
	  AND child.owner = LOWER(n.owner) AND child.repo = LOWER(n.repo)
	  AND child.ref = COALESCE(e.ref_value, '')
	  AND child.digest_algorithm = 'sha1' AND child.digest <> ''
	  AND LOWER(COALESCE(e.ref_value, '')) <> child.digest
	UNION
	SELECT n.node_key, n.owner, n.repo, COALESCE(n.path, ''), d.digest
	FROM reach
	CROSS JOIN abom_node_live s ON s.container_node_row_id = reach.node_row_id
	CROSS JOIN abom_edge_regular e ON e.src_node_row_id = s.row_id
	CROSS JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
	JOIN abom_lock_workflow_pin w ON w.repository = ?1 AND w.workflow_path = reach.workflow_path
	JOIN current_root c ON c.repository = LOWER(?1) AND c.commit_sha = LOWER(w.commit_sha)
	JOIN abom_lock_dependency d
	  ON d.repository = w.repository AND d.commit_sha = w.commit_sha AND d.pin_key = w.pin_key
	WHERE e.edge_kind = 'composite-step-uses-action'
	  AND n.is_external = 1 AND n.owner IS NOT NULL AND n.repo IS NOT NULL
	  AND d.owner = LOWER(n.owner) AND d.repo = LOWER(n.repo)
	  AND d.ref = COALESCE(e.ref_value, '')
	  AND d.digest_algorithm = 'sha1' AND d.digest <> ''
	  AND LOWER(COALESCE(e.ref_value, '')) <> d.digest
	ORDER BY 1, 5`
