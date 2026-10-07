package store

import (
	"database/sql"
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
	// DefaultValues is a multi-select's default: a repository that chose nothing carries each.
	DefaultValues []string `json:"defaultValues,omitempty"`
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

// ForgetPropertyValues drops what was recorded for repositories a read showed no declaration
// covers: their values were kept only to decide ownership, and describe nothing else.
func (s *Store) ForgetPropertyValues(repositories []string) error {
	if len(repositories) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range repositories {
		if _, err := tx.Exec(
			`DELETE FROM abom_repository_property WHERE repository_full_name = ?`, r); err != nil {
			return fmt.Errorf("forgetting the property values of %s: %w", r, err)
		}
	}
	return tx.Commit()
}

// PropertyValuesByRepository is every value on record, keyed by lower-cased repository name, so a
// declaration not yet stored can be judged against all of them in one read.
func (s *Store) PropertyValuesByRepository() (map[string][]PropertyValue, error) {
	rows, err := s.db.Query(`SELECT repository_full_name, property_name, value FROM abom_repository_property`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]PropertyValue{}
	for rows.Next() {
		var repo string
		var v PropertyValue
		if err := rows.Scan(&repo, &v.Name, &v.Value); err != nil {
			return nil, err
		}
		key := strings.ToLower(repo)
		out[key] = append(out[key], v)
	}
	return out, rows.Err()
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
// Counted against the owned repositories rather than against the property table, so that the parts
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
	// tier cannot come to different conclusions about the same repository: both read the default
	// branch's row, and a protected branch analyzed first does not make the repository analyzed.
	rows, err := s.db.Query(`
SELECT p.value, COUNT(DISTINCT r.repository_full_name) AS n,
       COUNT(DISTINCT CASE WHEN f.status = 'done' THEN r.repository_full_name END) AS analyzed
  FROM repository r
  JOIN abom_repository_property p ON p.repository_full_name = r.repository_full_name
  LEFT JOIN abom_frontier f
         ON f.depth = 0 AND f.ref = '' AND f.owner || '/' || f.repo = r.repository_full_name
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

	value, values, known, err := s.propertyDefault(name)
	if err != nil || !known {
		return out, err
	}
	schema := PropertyDefinition{HasDefault: values == nil, DefaultValue: value, DefaultValues: values}
	out.HasDefault, out.DefaultValue, out.DefaultValues = schema.HasDefault, schema.DefaultValue, schema.DefaultValues
	for i := range out.Values {
		out.Values[i].IsDefault = schema.IsDefault(out.Values[i].Value)
	}
	return out, nil
}

// SourceRepositoriesFor names the owned repositories a property filter selects as its sources.
//
// Only what the operator owns can be named. A grouping is read from the organization's custom
// properties, which are kept for owned repositories and read here only through them, so a
// dependency is never a source — it enters an answer by being reached from one.
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
	rows, err := s.db.Query(`SELECT DISTINCT p.property_name FROM abom_repository_property p
		WHERE ` + ownedFullName("p.repository_full_name") + ` ORDER BY p.property_name`)
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

// CodeQLConfigFile is the github-codeql-config-file value default setup applies to a repository:
// its own, or else its organization's default. Empty means none applies, which holds only where the
// organization declares no default: the values a listing reads are what GitHub applies.
func (s *Store) CodeQLConfigFile(repository string) (string, error) {
	var value sql.NullString
	err := s.db.QueryRow(`SELECT MIN(value) FROM abom_repository_property
		WHERE repository_full_name = ? AND property_name = 'github-codeql-config-file'`, repository).Scan(&value)
	if err != nil {
		return "", fmt.Errorf("reading the configuration file of %s: %w", repository, err)
	}
	if value.String != "" {
		return value.String, nil
	}
	owner, _, _ := strings.Cut(repository, "/")
	err = s.db.QueryRow(`SELECT MIN(default_value) FROM abom_property_schema
		WHERE owner = ? AND property_name = 'github-codeql-config-file'`, owner).Scan(&value)
	if err != nil {
		return "", fmt.Errorf("reading the default configuration file of %s: %w", owner, err)
	}
	return value.String, nil
}
