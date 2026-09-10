package store

import (
	"database/sql"
	"fmt"
	"time"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// savedViewDDL holds a named predicate, shaped like abom_owned_scope because it is the same kind
// of row: operator configuration, with provenance that is recorded rather than inferred.
const savedViewDDL = `
CREATE TABLE IF NOT EXISTS abom_saved_view (
  name       TEXT PRIMARY KEY,
  -- The canonical query string, normalized on the way in so two spellings of one question cannot
  -- compare unequal and a typo cannot survive until whichever consumer parses it last.
  predicate  TEXT NOT NULL,
  -- How the row arrived, decided by the code path that wrote it and never by its payload.
  -- NULL means nobody knows, which is the honest answer rather than a stand-in.
  created_by TEXT,
  created_at TEXT NOT NULL
);`

// SavedView is a named question, portable to anything that reads the predicate language.
type SavedView struct {
	Name      string `json:"name"`
	Predicate string `json:"predicate"`
	CreatedBy string `json:"createdBy,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// SaveView records a named predicate, replacing one of the same name.
//
// The predicate is parsed and re-serialized rather than stored verbatim: a view that cannot be
// parsed must fail here, for whoever wrote it, rather than in the middle of a pull request for
// whoever the gate is answering.
func (s *Store) SaveView(v SavedView) error {
	if v.Name == "" {
		return fmt.Errorf("store: a saved view needs a name")
	}
	if viewspec.IsBuiltin(v.Name) {
		return fmt.Errorf("store: %q is a built-in view and cannot be overwritten", v.Name)
	}
	spec, err := viewspec.Parse(v.Predicate)
	if err != nil {
		return fmt.Errorf("store: view %q does not name a predicate: %w", v.Name, err)
	}
	_, err = s.db.Exec(`
		INSERT INTO abom_saved_view (name, predicate, created_by, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			predicate = excluded.predicate,
			created_by = excluded.created_by,
			created_at = excluded.created_at`,
		v.Name, spec.String(), nullable(v.CreatedBy), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("saving view %q: %w", v.Name, err)
	}
	return nil
}

// SavedViews lists what the operator has stored, built-ins excluded: those are code.
func (s *Store) SavedViews() ([]SavedView, error) {
	return queryAll(s, "reading saved views", func(r *sql.Rows) (SavedView, error) {
		var v SavedView
		var by sql.NullString
		err := r.Scan(&v.Name, &v.Predicate, &by, &v.CreatedAt)
		v.CreatedBy = by.String
		return v, err
	}, `SELECT name, predicate, created_by, created_at
	      FROM abom_saved_view ORDER BY name`)
}

// SavedViewNamed reads one stored view, reporting whether it exists rather than returning an
// empty predicate that would match everything.
func (s *Store) SavedViewNamed(name string) (SavedView, bool, error) {
	var v SavedView
	var by sql.NullString
	err := s.db.QueryRow(
		`SELECT name, predicate, created_by, created_at FROM abom_saved_view WHERE name = ?`,
		name).Scan(&v.Name, &v.Predicate, &by, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return SavedView{}, false, nil
	}
	if err != nil {
		return SavedView{}, false, fmt.Errorf("reading view %q: %w", name, err)
	}
	v.CreatedBy = by.String
	return v, true, nil
}

func (s *Store) DeleteView(name string) error {
	if viewspec.IsBuiltin(name) {
		return fmt.Errorf("store: %q is a built-in view and cannot be deleted", name)
	}
	if _, err := s.db.Exec(`DELETE FROM abom_saved_view WHERE name = ?`, name); err != nil {
		return fmt.Errorf("deleting view %q: %w", name, err)
	}
	return nil
}
