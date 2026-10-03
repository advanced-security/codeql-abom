package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ClosureManifest is what one closure database was built from.
//
// The runs a closure records carry only its digest, and a digest cannot be read back. This is the
// other half: which repositories at which commits went in, so a finding seen through a closure can
// still say how to reproduce it.
type ClosureManifest struct {
	Digest     string
	Root       string
	RootCommit string
	Anchored   bool
	Members    []ClosureMember
}

// ClosureMember is one callee a closure staged.
type ClosureMember struct {
	Repository string
	Commit     string
	Kind       string
	// Path is the file's path in its own repository; StagedPath is where the closure put it.
	Path       string
	StagedPath string
}

// RecordClosure keeps a manifest, once. The digest is computed from these same inputs, so a
// second recording under one digest can only restate the first.
func (s *Store) RecordClosure(m ClosureManifest) error {
	if m.Digest == "" || m.Root == "" || m.RootCommit == "" {
		return fmt.Errorf("store: a closure manifest needs a digest, a root and its commit")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO abom_closure (digest, root, root_commit, anchored, recorded_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT(digest) DO NOTHING`,
		m.Digest, m.Root, m.RootCommit, m.Anchored, now()); err != nil {
		return fmt.Errorf("recording closure %s: %w", m.Digest, err)
	}
	for _, member := range m.Members {
		if _, err := tx.Exec(`INSERT INTO abom_closure_member
			(digest, staged_path, repository, commit_sha, kind, path) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(digest, staged_path) DO NOTHING`,
			m.Digest, member.StagedPath, member.Repository, member.Commit, member.Kind,
			member.Path); err != nil {
			return fmt.Errorf("recording what closure %s staged: %w", m.Digest, err)
		}
	}
	return tx.Commit()
}

// ClosureManifestOf reads a manifest back. An unknown digest is not an error: it is a closure
// nobody recorded, which the caller reports.
func (s *Store) ClosureManifestOf(digest string) (ClosureManifest, bool, error) {
	m := ClosureManifest{Digest: digest, Members: []ClosureMember{}}
	err := s.db.QueryRow(`SELECT root, root_commit, anchored FROM abom_closure WHERE digest = ?`,
		digest).Scan(&m.Root, &m.RootCommit, &m.Anchored)
	if errors.Is(err, sql.ErrNoRows) {
		return ClosureManifest{}, false, nil
	}
	if err != nil {
		return ClosureManifest{}, false, fmt.Errorf("reading closure %s: %w", digest, err)
	}
	rows, err := s.db.Query(`SELECT repository, commit_sha, kind, path, staged_path
		FROM abom_closure_member WHERE digest = ? ORDER BY staged_path`, digest)
	if err != nil {
		return ClosureManifest{}, false, fmt.Errorf("reading what closure %s staged: %w", digest, err)
	}
	defer rows.Close()
	for rows.Next() {
		var member ClosureMember
		if err := rows.Scan(&member.Repository, &member.Commit, &member.Kind, &member.Path,
			&member.StagedPath); err != nil {
			return ClosureManifest{}, false, err
		}
		m.Members = append(m.Members, member)
	}
	return m, true, rows.Err()
}

// RepositoryNamed finds a repository the corpus already holds, by any spelling, and reports how
// the corpus spells it. It never creates one: a row created here would be a repository nothing
// analyzed, and every total that counts repositories would count it.
func (s *Store) RepositoryNamed(fullName string) (int64, string, bool, error) {
	var id int64
	var name string
	// The exact spelling first, which the unique index answers; any other case only after.
	err := s.db.QueryRow(`SELECT row_id, repository_full_name FROM repository
		WHERE repository_full_name = ?`, fullName).Scan(&id, &name)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.db.QueryRow(`SELECT row_id, repository_full_name FROM repository
			WHERE repository_full_name = ? COLLATE NOCASE ORDER BY row_id LIMIT 1`,
			fullName).Scan(&id, &name)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, fmt.Errorf("finding repository %q: %w", fullName, err)
	}
	return id, name, true, nil
}

// AnalyzedCommit is the commit the crawl analyzed for one queue row: what the regular graph
// describes for that dependency at that reference. A row nobody has analyzed has none, which is
// not a commit of "".
func (s *Store) AnalyzedCommit(nodeKey, ref string) (string, bool, error) {
	var commit sql.NullString
	// The exact key first, which the primary key answers; a repository's own row keeps the case
	// it was enumerated in, so any other case only after.
	err := s.db.QueryRow(`SELECT resolved_sha FROM abom_frontier
		WHERE node_key = ? AND ref = ?`, nodeKey, ref).Scan(&commit)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.db.QueryRow(`SELECT resolved_sha FROM abom_frontier
			WHERE node_key = ? COLLATE NOCASE AND ref = ?
			ORDER BY resolved_sha IS NULL, analyzed_at DESC LIMIT 1`, nodeKey, ref).Scan(&commit)
	}
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!commit.Valid || commit.String == "") {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading what %s@%s was analyzed at: %w", nodeKey, ref, err)
	}
	return strings.ToLower(commit.String), true, nil
}

// ClosureTally is what one closure run reported, measured against what was already on record.
type ClosureTally struct {
	// Findings is every finding the run sighted.
	Findings int
	// Shared were already reported by a regular analysis: one defect, now seen two ways.
	Shared int
	// Unattached carry no node, so no policy can see them.
	Unattached int
}

// ExtendedOnly are findings only an extended analysis reports.
func (t ClosureTally) ExtendedOnly() int { return t.Findings - t.Shared }

// ClosureTallyOf measures one closure run's findings.
func (s *Store) ClosureTallyOf(runID int64) (ClosureTally, error) {
	var t ClosureTally
	err := s.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(f.row_id IN (SELECT r.row_id FROM abom_finding_regular r)), 0),
		       COALESCE(SUM(NOT EXISTS (SELECT 1 FROM abom_finding_node fn
		                                 WHERE fn.finding_row_id = f.row_id)), 0)
		  FROM abom_finding_extended f
		 WHERE f.row_id IN (SELECT sg.finding_row_id FROM abom_finding_sighting sg
		                     WHERE sg.query_run_id = ?)`, runID).
		Scan(&t.Findings, &t.Shared, &t.Unattached)
	if err != nil {
		return ClosureTally{}, fmt.Errorf("measuring closure run %d: %w", runID, err)
	}
	return t, nil
}
