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
  -- How the Explorer drew it. Opaque: this is browser rendering, not a predicate, and NULL means
  -- nobody said rather than "every default".
  presentation TEXT,
  -- How the row arrived, decided by the code path that wrote it and never by its payload.
  -- NULL means nobody knows, which is the honest answer rather than a stand-in.
  created_by TEXT,
  created_at TEXT NOT NULL,
  -- The revision of the filter language the predicate was kept in. NULL is a view kept before
  -- revisions were recorded, when scope=owned asked what scope=supply-chain asks now.
  vocabulary INTEGER
);`

// savedViewVocabulary is the filter language revision a view is kept in now: the first with
// `scope=supply-chain`, and with `scope=owned` holding only what the declared repositories hold.
const savedViewVocabulary = 1

// SavedView is a named question, portable to anything that reads the predicate language.
type SavedView struct {
	Name      string `json:"name"`
	Predicate string `json:"predicate"`
	// Presentation is how the Explorer drew it: view, grouping, depth, direction. Opaque text,
	// never parsed here — it is browser rendering, and a store with an opinion about it would make
	// every new knob a migration. Empty means nobody said, which is not the same as every default.
	Presentation string `json:"presentation,omitempty"`
	CreatedBy    string `json:"createdBy,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
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
		INSERT INTO abom_saved_view (name, predicate, presentation, created_by, created_at, vocabulary)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			predicate = excluded.predicate,
			presentation = excluded.presentation,
			created_by = excluded.created_by,
			created_at = excluded.created_at,
			vocabulary = excluded.vocabulary`,
		v.Name, spec.String(), nullable(v.Presentation), nullable(v.CreatedBy),
		time.Now().UTC().Format(time.RFC3339), savedViewVocabulary)
	if err != nil {
		return fmt.Errorf("saving view %q: %w", v.Name, err)
	}
	return nil
}

// SavedViews lists what the operator has stored, built-ins excluded: those are code.
func (s *Store) SavedViews() ([]SavedView, error) {
	return queryAll(s, "reading saved views", func(r *sql.Rows) (SavedView, error) {
		var v SavedView
		var by, drawn sql.NullString
		err := r.Scan(&v.Name, &v.Predicate, &drawn, &by, &v.CreatedAt)
		v.Presentation = drawn.String
		v.CreatedBy = by.String
		return v, err
	}, `SELECT name, predicate, presentation, created_by, created_at
	      FROM abom_saved_view ORDER BY name`)
}

// SavedViewNamed reads one stored view, reporting whether it exists rather than returning an
// empty predicate that would match everything.
func (s *Store) SavedViewNamed(name string) (SavedView, bool, error) {
	var v SavedView
	var by, drawn sql.NullString
	err := s.db.QueryRow(
		`SELECT name, predicate, presentation, created_by, created_at
		   FROM abom_saved_view WHERE name = ?`,
		name).Scan(&v.Name, &v.Predicate, &drawn, &by, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return SavedView{}, false, nil
	}
	if err != nil {
		return SavedView{}, false, fmt.Errorf("reading view %q: %w", name, err)
	}
	v.Presentation = drawn.String
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

// upgradeSavedViews carries each view kept before the vocabulary was recorded into the current one,
// once. `scope=owned` asked the closure then, which `scope=supply-chain` asks now, so the view keeps
// asking what its author asked. A view whose name a built-in has since taken is renamed: left alone
// it would be listed twice, opened as the built-in, and impossible to forget.
func (s *Store) upgradeSavedViews() error {
	type kept struct{ name, predicate string }
	views, err := queryAll(s, "reading views kept before the vocabulary was recorded",
		func(r *sql.Rows) (kept, error) {
			var k kept
			return k, r.Scan(&k.name, &k.predicate)
		}, `SELECT name, predicate FROM abom_saved_view WHERE vocabulary IS NULL ORDER BY name`)
	if err != nil {
		return err
	}
	for _, v := range views {
		predicate := v.predicate
		if spec, err := viewspec.Parse(v.predicate); err == nil && spec.Scope == viewspec.ScopeOwned {
			spec.Scope = viewspec.ScopeSupplyChain
			predicate = spec.String()
		}
		name := v.name
		if viewspec.IsBuiltin(name) {
			if name, err = s.unusedViewName(name + "-kept"); err != nil {
				return err
			}
		}
		if _, err := s.db.Exec(`
			UPDATE abom_saved_view SET name = ?, predicate = ?, vocabulary = ? WHERE name = ?`,
			name, predicate, savedViewVocabulary, v.name); err != nil {
			return fmt.Errorf("upgrading saved view %q: %w", v.name, err)
		}
	}
	return nil
}

// unusedViewName is base, or base with the first number that names no kept view.
func (s *Store) unusedViewName(base string) (string, error) {
	name := base
	for i := 2; ; i++ {
		var taken bool
		if err := s.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM abom_saved_view WHERE name = ?)`,
			name).Scan(&taken); err != nil {
			return "", fmt.Errorf("checking saved view name %q: %w", name, err)
		}
		if !taken && !viewspec.IsBuiltin(name) {
			return name, nil
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}
