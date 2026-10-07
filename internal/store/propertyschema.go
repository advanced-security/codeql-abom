package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// propertySchemaDDL holds what the organization declared a property to be, as opposed to what any
// repository happens to carry.
//
// It exists for two reasons. A required property with a default has no unset state: every
// repository carries a value whether anybody chose one or not, so a large bucket can be the one
// nobody picked, and without the schema there is no way to tell that apart from a group teams
// deliberately joined. And who may set a property's values decides whether it may scope ownership.
// See docs/vocabulary.md.
const propertySchemaDDL = `
CREATE TABLE IF NOT EXISTS abom_property_schema (
  owner         TEXT NOT NULL COLLATE NOCASE,
  property_name TEXT NOT NULL COLLATE NOCASE,
  -- NULL means the organization declared no default, so a repository genuinely can be unset.
  -- An absent row means nobody has read the schema, which is a third state and not this one.
  default_value TEXT,
  -- A multi-select's default, a JSON list, since a repository that chose nothing carries each.
  default_values TEXT,
  -- GitHub's value_type and values_editable_by; NULL when the read did not say, never "anybody".
  value_type         TEXT,
  values_editable_by TEXT,
  PRIMARY KEY (owner, property_name)
);
-- When an account's schema was last read whole. Absent means never, which holds a property scope.
CREATE TABLE IF NOT EXISTS abom_property_schema_read (
  owner   TEXT PRIMARY KEY COLLATE NOCASE,
  read_at TEXT NOT NULL
);`

// PropertyDefinition is one property as the organization declares it.
type PropertyDefinition struct {
	Name string
	// HasDefault separates "no default declared" from "the default is the empty string", which
	// are different claims about what a repository gets for doing nothing.
	HasDefault   bool
	DefaultValue string
	// DefaultValues is a multi-select's default: a repository that chose nothing carries all of them.
	DefaultValues []string
	// ValueType and ValuesEditableBy are GitHub's words; empty when the read did not say.
	ValueType        string
	ValuesEditableBy string
}

// IsDefault reports whether a repository that chose nothing carries this value.
func (d PropertyDefinition) IsDefault(value string) bool {
	return (d.HasDefault && d.DefaultValue == value) || slices.Contains(d.DefaultValues, value)
}

// PropertyEditableByOrganization is the one `values_editable_by` that lets a property scope
// ownership: only organization owners and admins can set its values.
const PropertyEditableByOrganization = "org_actors"

// ReplacePropertySchema records what one account declares its properties to be.
func (s *Store) ReplacePropertySchema(owner string, defs []PropertyDefinition) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM abom_property_schema WHERE owner = ?`, owner); err != nil {
		return err
	}
	for _, d := range defs {
		if d.Name == "" {
			continue
		}
		var value, values any
		if d.HasDefault {
			value = d.DefaultValue
		}
		if len(d.DefaultValues) > 0 {
			list, err := json.Marshal(d.DefaultValues)
			if err != nil {
				return err
			}
			values = string(list)
		}
		if _, err := tx.Exec(`
INSERT INTO abom_property_schema (owner, property_name, default_value, default_values, value_type, values_editable_by)
VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))
ON CONFLICT(owner, property_name) DO UPDATE SET default_value = excluded.default_value,
  default_values = excluded.default_values,
  value_type = excluded.value_type, values_editable_by = excluded.values_editable_by`,
			owner, d.Name, value, values, d.ValueType, d.ValuesEditableBy); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
INSERT INTO abom_property_schema_read (owner, read_at) VALUES (?, ?)
ON CONFLICT(owner) DO UPDATE SET read_at = excluded.read_at`, owner, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// PropertySchema reads what one account declares its properties to be, as last read.
func (s *Store) PropertySchema(owner string) ([]PropertyDefinition, error) {
	return queryAll(s, "reading the property schema", func(r *sql.Rows) (PropertyDefinition, error) {
		var d PropertyDefinition
		var def, defs, kind, editable sql.NullString
		if err := r.Scan(&d.Name, &def, &defs, &kind, &editable); err != nil {
			return d, err
		}
		d.HasDefault, d.DefaultValue = def.Valid, def.String
		d.ValueType, d.ValuesEditableBy = kind.String, editable.String
		if defs.Valid {
			if err := json.Unmarshal([]byte(defs.String), &d.DefaultValues); err != nil {
				return d, fmt.Errorf("reading the defaults of %s: %w", d.Name, err)
			}
		}
		return d, nil
	}, `SELECT property_name, default_value, default_values, value_type, values_editable_by
	      FROM abom_property_schema WHERE owner = ? ORDER BY property_name`, owner)
}

// PropertySchemaReadAt reports when an account's schema was last read, and whether it ever was.
func (s *Store) PropertySchemaReadAt(owner string) (time.Time, bool, error) {
	var at string
	err := s.db.QueryRow(`SELECT read_at FROM abom_property_schema_read WHERE owner = ?`, owner).Scan(&at)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("reading when %s's property schema was read: %w", owner, err)
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	return t, err == nil, err
}

// propertyDefault reports the default one key carries: a single value, or a multi-select's list,
// and whether a schema said anything at all.
//
// Across accounts rather than per account, because the breakdown is over every declared account at
// once. Two accounts declaring different defaults for one key leave it unknown: labeling a bucket
// as "nobody chose this" is only honest when it is true everywhere the bucket is counted.
func (s *Store) propertyDefault(name string) (value string, values []string, known bool, err error) {
	rows, err := s.db.Query(`SELECT DISTINCT default_value, default_values
		FROM abom_property_schema WHERE property_name = ?`, name)
	if err != nil {
		return "", nil, false, err
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var one, many *string
		if err := rows.Scan(&one, &many); err != nil {
			return "", nil, false, err
		}
		seen++
		switch {
		case one != nil:
			value = *one
		case many != nil:
			if err := json.Unmarshal([]byte(*many), &values); err != nil {
				return "", nil, false, fmt.Errorf("reading the defaults of %s: %w", name, err)
			}
		default:
			// The schema was read and declared no default, so there is nothing to label.
			return "", nil, false, rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return "", nil, false, err
	}
	return value, values, seen == 1, nil
}
