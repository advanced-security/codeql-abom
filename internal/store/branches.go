package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// SetDefaultBranch records the branch a repository currently builds from.
//
// This is a *current* fact, unlike the ref an analysis recorded, which is historical. A branch
// rename changes this and must not retroactively rewrite what was analyzed, which is why the two
// live apart and neither is derived from the other.
func (s *Store) SetDefaultBranch(fullName, branch string) error {
	if fullName == "" || branch == "" {
		return fmt.Errorf("store: SetDefaultBranch needs a repository and a branch")
	}
	if _, err := s.db.Exec(`
		UPDATE repository SET default_branch = ? WHERE repository_full_name = ?`,
		branch, fullName); err != nil {
		return fmt.Errorf("recording default branch for %s: %w", fullName, err)
	}
	return nil
}

// DefaultBranch reports the branch a repository builds from, and whether one is on record.
//
// The boolean is the point: a repository nobody has enumerated has no default branch recorded,
// and answering "main" for it would state something nobody observed.
func (s *Store) DefaultBranch(fullName string) (string, bool, error) {
	var branch sql.NullString
	err := s.db.QueryRow(
		`SELECT default_branch FROM repository WHERE repository_full_name = ?`,
		fullName).Scan(&branch)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading default branch for %s: %w", fullName, err)
	}
	return branch.String, branch.Valid && branch.String != "", nil
}

// SetProtectedBranches replaces what is known about a repository's protected branches, and records
// that the question was asked.
//
// Replaces rather than adds: protection is removed as well as granted, so merging would leave a
// branch reported as protected long after it stopped being. The whole answer is the answer.
//
// An empty list is a legitimate answer and is stored as one, which is what makes it different from
// never having asked.
//
// An owned repository is live at each protected branch, so recording the answer also queues each
// one beside the default branch's empty-ref row, at depth 0, and removes the row of a branch that
// lost protection. That is what puts a branch's commit in every walk and takes it out again; see
// queueProtectedBranches.
func (s *Store) SetProtectedBranches(fullName string, branches []string) (BranchQueue, error) {
	var out BranchQueue
	if fullName == "" {
		return out, fmt.Errorf("store: SetProtectedBranches needs a repository")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return out, fmt.Errorf("recording protected branches for %s: %w", fullName, err)
	}
	defer tx.Rollback()

	var id int64
	var name string
	var defaultBranch sql.NullString
	if err := tx.QueryRow(`
		SELECT row_id, repository_full_name, default_branch
		  FROM repository WHERE repository_full_name = ?`, fullName).
		Scan(&id, &name, &defaultBranch); err != nil {
		return out, fmt.Errorf("resolving %s: %w", fullName, err)
	}

	if _, err := tx.Exec(
		`DELETE FROM abom_protected_branch WHERE repository_row_id = ?`, id); err != nil {
		return out, fmt.Errorf("clearing protected branches for %s: %w", fullName, err)
	}
	for _, branch := range branches {
		if branch == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO abom_protected_branch (repository_row_id, branch_name)
			VALUES (?, ?) ON CONFLICT(repository_row_id, branch_name) DO NOTHING`,
			id, branch); err != nil {
			return out, fmt.Errorf("recording protected branch %s of %s: %w", branch, fullName, err)
		}
	}
	// The marker is what separates "protects nothing" from "nobody asked", so it is written in
	// the same transaction as the branches themselves.
	if _, err := tx.Exec(
		`UPDATE repository SET branches_checked_at = ? WHERE row_id = ?`, now(), id); err != nil {
		return out, fmt.Errorf("recording the branch check for %s: %w", fullName, err)
	}
	out, err = queueProtectedBranches(tx, id, name, defaultBranch.String, branches)
	if err != nil {
		return BranchQueue{}, err
	}
	return out, tx.Commit()
}

// BranchQueue is what recording a repository's protected branches did to the analysis queue.
type BranchQueue struct {
	// Queued counts branches that gained a row of their own.
	Queued int `json:"queued"`
	// Removed counts rows deleted because their branch stopped being protected.
	Removed int `json:"removed"`
}

// protectedBranchRow is a queue row standing for a protected branch: the repository's own key at
// depth 0 with a reference. Every other writer queues an empty ref at depth 0, or depth 1 or more.
const protectedBranchRow = `node_key = ? AND depth = 0 AND ref <> ''`

// queueProtectedBranches makes the repository's protected-branch rows match the answer just
// recorded.
//
// Only an owned repository is live at its protected branches, and only one whose default branch is
// known: that branch is already the empty-ref row, and while it is unknown any branch may be the
// one a second row would analyze again. A branch losing protection loses its row, deleted rather
// than tombstoned because the frontier is a work queue, as the prune does. The exception is a row a
// dependency also names, which is somebody's dependency still and keeps the depth its referrer
// gives it.
func queueProtectedBranches(
	tx *sql.Tx, id int64, fullName, defaultBranch string, branches []string,
) (BranchQueue, error) {
	var out BranchQueue
	want := map[string]bool{}
	var owned bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM repository r WHERE r.row_id = ? AND `+
		ownedFullName("r.repository_full_name")+`)`, id).Scan(&owned); err != nil {
		return out, fmt.Errorf("reading whether %s is owned: %w", fullName, err)
	}
	if owned && defaultBranch != "" {
		for _, branch := range branches {
			if branch != "" && branch != defaultBranch {
				want[branch] = true
			}
		}
	}

	queued, err := protectedBranchRows(tx, fullName)
	if err != nil {
		return out, err
	}
	have := map[string]bool{}
	for _, ref := range queued {
		have[ref] = true
		if want[ref] {
			continue
		}
		removed, err := dequeueProtectedBranch(tx, fullName, ref)
		if err != nil {
			return out, err
		}
		if removed {
			out.Removed++
		}
	}

	owner, repo, _ := splitRepository(fullName)
	at := now()
	for _, branch := range sortedKeys(want) {
		if have[branch] {
			continue
		}
		// A row a dependency already holds becomes the branch's too: the depth only falls, as an
		// enqueue does, and running in the operator's own repository makes it runtime.
		if _, err := tx.Exec(`
			INSERT INTO abom_frontier
				(node_key, ref, owner, repo, depth, status, enqueued_at, supply_chain)
			VALUES (?, ?, ?, ?, 0, 'pending', ?, 'runtime')
			ON CONFLICT(node_key, ref) DO UPDATE SET depth = 0, supply_chain = 'runtime'`,
			fullName, branch, owner, repo, at); err != nil {
			return out, fmt.Errorf("queueing protected branch %s of %s: %w", branch, fullName, err)
		}
		out.Queued++
	}
	return out, nil
}

// protectedBranchRows names the references a repository is queued at as protected branches.
func protectedBranchRows(tx *sql.Tx, fullName string) ([]string, error) {
	rows, err := tx.Query(
		`SELECT ref FROM abom_frontier WHERE `+protectedBranchRow+` ORDER BY ref`, fullName)
	if err != nil {
		return nil, fmt.Errorf("reading the protected branches queued for %s: %w", fullName, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("scanning a protected branch queued for %s: %w", fullName, err)
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// dequeueProtectedBranch removes a branch's row unless a dependency still names the repository at
// that reference, and reports whether it did.
//
// A kept row takes its depth from the queue rows of the repositories naming it, one hop further;
// a referrer with no row of its own is a source, which puts the dependency at depth 1.
func dequeueProtectedBranch(tx *sql.Tx, fullName, ref string) (bool, error) {
	var named bool
	var referrer sql.NullInt64
	if err := tx.QueryRow(`
		SELECT COUNT(e.row_id) > 0, MIN(rf.depth)
		  FROM abom_edge_regular e
		  JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
		  JOIN repository r ON r.row_id = e.repository_row_id
		  LEFT JOIN abom_frontier rf
		         ON LOWER(rf.owner || '/' || rf.repo) = LOWER(r.repository_full_name)
		        AND NOT (rf.node_key = n.node_key AND rf.ref = COALESCE(e.ref_value, ''))
		 WHERE n.node_key = ? AND COALESCE(e.ref_value, '') = ? AND `+DependencyEdgeFilter,
		fullName, ref).Scan(&named, &referrer); err != nil {
		return false, fmt.Errorf("reading what still names %s@%s: %w", fullName, ref, err)
	}
	if named {
		if _, err := tx.Exec(`UPDATE abom_frontier SET depth = ? WHERE node_key = ? AND ref = ?`,
			referrer.Int64+1, fullName, ref); err != nil {
			return false, fmt.Errorf("keeping %s@%s as a dependency: %w", fullName, ref, err)
		}
		return false, nil
	}
	if _, err := tx.Exec(`DELETE FROM abom_frontier WHERE node_key = ? AND ref = ?`,
		fullName, ref); err != nil {
		return false, fmt.Errorf("dequeueing %s@%s: %w", fullName, ref, err)
	}
	return true, nil
}

// ProtectedBranches reports a repository's protected branches, and whether anyone has looked.
//
// Absent is not unprotected. A gate that reads "no protected branches" from a repository nobody
// asked about would report a policy as satisfied because it was never evaluated.
func (s *Store) ProtectedBranches(fullName string) ([]string, bool, error) {
	var id int64
	var checked sql.NullString
	err := s.db.QueryRow(`
		SELECT row_id, branches_checked_at FROM repository WHERE repository_full_name = ?`,
		fullName).Scan(&id, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading protected branches for %s: %w", fullName, err)
	}
	if !checked.Valid {
		return nil, false, nil
	}

	rows, err := s.db.Query(`
		SELECT branch_name FROM abom_protected_branch
		WHERE repository_row_id = ? ORDER BY branch_name`, id)
	if err != nil {
		return nil, false, fmt.Errorf("reading protected branches for %s: %w", fullName, err)
	}
	defer rows.Close()

	branches := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, false, fmt.Errorf("scanning protected branch of %s: %w", fullName, err)
		}
		branches = append(branches, name)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("reading protected branches for %s: %w", fullName, err)
	}
	return branches, true, nil
}
