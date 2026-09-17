package store

import (
	"fmt"
	"strings"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// repositoryPropertyDDL holds what GitHub says a repository is, as the organization administers it.
//
// One row per value, not per property: a multi-select property matches if any of its values
// matches, and that is a plain join only when each value stands alone. Comma-joining them into one
// column would make a value containing a comma impossible to tell from two values, which is a
// limitation stated upstream and not one worth reproducing.
//
// An absent row means the organization sets nothing, and that is its own state rather than a
// default bucket. Whether anything has ever been read at all is answered by
// `abom_enumeration_state`, because the values arrive on the same walk as the listing.
// See docs/vocabulary.md.
const repositoryPropertyDDL = `
CREATE TABLE IF NOT EXISTS abom_repository_property (
  repository_full_name TEXT NOT NULL COLLATE NOCASE,
  -- GitHub matches property names case-insensitively, so two spellings must not become two keys.
  property_name        TEXT NOT NULL COLLATE NOCASE,
  value                TEXT NOT NULL,
  PRIMARY KEY (repository_full_name, property_name, value)
);
CREATE INDEX IF NOT EXISTS idx_repository_property_name
  ON abom_repository_property (property_name, value);`

// PropertyValue is one custom property value GitHub holds for a repository.
type PropertyValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PropertyBreakdown is how one key divides the repositories the operator owns.
type PropertyBreakdown struct {
	Name string `json:"name"`
	// Repositories is every owned repository, which is what the parts are checked against.
	Repositories int `json:"repositories"`
	// WithValue and WithoutValue partition that total. A repository carrying two values for one
	// key belongs to both, so the per-value counts may sum past WithValue; these two do not.
	WithValue    int `json:"withValue"`
	WithoutValue int `json:"withoutValue"`
	// Values is empty for a key no repository sets, which is reported rather than dropped: it is
	// how an operator finds out they configured a key the organization does not administer.
	Values []PropertyCount `json:"values"`
	// DefaultValue is what a repository carries for doing nothing, and HasDefault says whether a
	// schema established that at all. A required property with a default has no unset state, so
	// WithoutValue is zero and the largest bucket may be the one nobody picked.
	HasDefault   bool   `json:"hasDefault"`
	DefaultValue string `json:"defaultValue,omitempty"`
}

// PropertyCount is one distinct value and how many repositories carry it.
type PropertyCount struct {
	Value        string `json:"value"`
	Repositories int    `json:"repositories"`
	// Analyzed is how many of them have been examined, read the same way TierCounts reads it: a
	// depth-zero frontier row that is done. A repository nothing has queued counts toward the
	// unit and not toward this, because it has not been examined and found clean.
	Analyzed int `json:"analyzed"`
	// IsDefault marks the bucket a repository lands in by not choosing. Counting it like any
	// other unit tells a reader that eight teams picked it when none of them did.
	IsDefault bool `json:"isDefault"`
}

// ReplacePropertyValues records everything the organization sets for one repository.
//
// Replace rather than merge: a property removed upstream has to disappear here, or the corpus keeps
// reporting a repository in a unit it was taken out of, and nothing would ever correct it.
func (s *Store) ReplacePropertyValues(repository string, values []PropertyValue) error {
	if repository == "" {
		return fmt.Errorf("store: ReplacePropertyValues needs a repository")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`DELETE FROM abom_repository_property WHERE repository_full_name = ?`, repository); err != nil {
		return err
	}
	for _, v := range values {
		if v.Name == "" {
			continue
		}
		if _, err := tx.Exec(`
INSERT INTO abom_repository_property (repository_full_name, property_name, value)
VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, repository, v.Name, v.Value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PropertyValues reports what the organization sets for one repository.
func (s *Store) PropertyValues(repository string) ([]PropertyValue, error) {
	rows, err := s.db.Query(`
SELECT property_name, value FROM abom_repository_property
 WHERE repository_full_name = ? ORDER BY property_name, value`, repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PropertyValue{}
	for rows.Next() {
		var v PropertyValue
		if err := rows.Scan(&v.Name, &v.Value); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PropertyBreakdown reports how one key divides the operator's repositories.
//
// Counted against `abom_owned_repository` rather than against the property table, so that the parts
// can be reconciled with a whole that exists independently of them. A breakdown that only counts
// what it found cannot tell a reader what it missed.
func (s *Store) PropertyBreakdown(name string) (PropertyBreakdown, error) {
	out := PropertyBreakdown{Name: name, Values: []PropertyCount{}}
	owned, err := s.OwnedRepositories()
	if err != nil {
		return out, err
	}
	out.Repositories = len(owned)

	if err := s.db.QueryRow(`
SELECT COUNT(DISTINCT r.repository_full_name)
  FROM repository r
  JOIN abom_repository_property p ON p.repository_full_name = r.repository_full_name
 WHERE p.property_name = ?
   AND `+ownedFullName("r.repository_full_name"), name).Scan(&out.WithValue); err != nil {
		return out, err
	}
	out.WithoutValue = out.Repositories - out.WithValue

	// Analyzed is counted from the frontier rather than from a column of its own, so a unit and a
	// tier cannot come to different conclusions about the same repository.
	rows, err := s.db.Query(`
SELECT p.value, COUNT(DISTINCT r.repository_full_name) AS n,
       COUNT(DISTINCT CASE WHEN f.status = 'done' THEN r.repository_full_name END) AS analyzed
  FROM repository r
  JOIN abom_repository_property p ON p.repository_full_name = r.repository_full_name
  LEFT JOIN abom_frontier f
         ON f.depth = 0 AND f.owner || '/' || f.repo = r.repository_full_name
 WHERE p.property_name = ?
   AND `+ownedFullName("r.repository_full_name")+`
 GROUP BY p.value
 ORDER BY n DESC, p.value`, name)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	for rows.Next() {
		var c PropertyCount
		if err := rows.Scan(&c.Value, &c.Repositories, &c.Analyzed); err != nil {
			return out, err
		}
		out.Values = append(out.Values, c)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	out.DefaultValue, out.HasDefault, err = s.propertyDefault(name)
	if err != nil {
		return out, err
	}
	if out.HasDefault {
		for i := range out.Values {
			out.Values[i].IsDefault = out.Values[i].Value == out.DefaultValue
		}
	}
	return out, nil
}

// SourceRepositoriesFor names the owned repositories a property filter selects as its sources.
//
// Only what the operator owns can be named. A grouping is read from the organization's custom
// properties, which are collected for owned repositories and nowhere else, so a dependency is
// never a source — it enters an answer by being reached from one.
//
// Exclusion is taken here, over the sources, and not over every node the walk later reaches: a
// repository one group owns and another group depends on is still that second group's dependency,
// and removing it would answer "what does this group depend on" by deleting part of the answer.
func (s *Store) SourceRepositoriesFor(include, exclude []viewspec.PropertyMatch) ([]string, error) {
	owned, err := s.OwnedRepositories()
	if err != nil {
		return nil, err
	}
	members, err := s.repositoriesMatching(include)
	if err != nil {
		return nil, err
	}
	dropped, err := s.repositoriesMatching(exclude)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(owned))
	for _, name := range owned {
		low := strings.ToLower(name)
		if len(include) > 0 && !members[low] {
			continue
		}
		if dropped[low] {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

// PropertyKeys names every key the corpus has seen a value for, so an operator configuring one can
// be shown what the organization actually administers rather than asked to remember it.
func (s *Store) PropertyKeys() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT property_name FROM abom_repository_property ORDER BY property_name`)
	if err != nil {
		return nil, err
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
