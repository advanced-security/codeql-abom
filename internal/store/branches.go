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
func (s *Store) SetProtectedBranches(fullName string, branches []string) error {
	if fullName == "" {
		return fmt.Errorf("store: SetProtectedBranches needs a repository")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("recording protected branches for %s: %w", fullName, err)
	}
	defer tx.Rollback()

	var id int64
	if err := tx.QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = ?`, fullName).Scan(&id); err != nil {
		return fmt.Errorf("resolving %s: %w", fullName, err)
	}

	if _, err := tx.Exec(
		`DELETE FROM abom_protected_branch WHERE repository_row_id = ?`, id); err != nil {
		return fmt.Errorf("clearing protected branches for %s: %w", fullName, err)
	}
	for _, branch := range branches {
		if branch == "" {
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO abom_protected_branch (repository_row_id, branch_name)
			VALUES (?, ?) ON CONFLICT(repository_row_id, branch_name) DO NOTHING`,
			id, branch); err != nil {
			return fmt.Errorf("recording protected branch %s of %s: %w", branch, fullName, err)
		}
	}
	// The marker is what separates "protects nothing" from "nobody asked", so it is written in
	// the same transaction as the branches themselves.
	if _, err := tx.Exec(
		`UPDATE repository SET branches_checked_at = ? WHERE row_id = ?`, now(), id); err != nil {
		return fmt.Errorf("recording the branch check for %s: %w", fullName, err)
	}
	return tx.Commit()
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
