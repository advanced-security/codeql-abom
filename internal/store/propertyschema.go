package store

// propertySchemaDDL holds what the organization declared a property to be, as opposed to what any
// repository happens to carry.
//
// It exists for one reason: a required property with a default has no unset state. Every repository
// carries a value whether anybody chose one or not, so a large bucket can be the one nobody picked.
// Without the schema there is no way to tell that apart from a group teams deliberately joined.
// See docs/vocabulary.md.
const propertySchemaDDL = `
CREATE TABLE IF NOT EXISTS abom_property_schema (
  owner         TEXT NOT NULL COLLATE NOCASE,
  property_name TEXT NOT NULL COLLATE NOCASE,
  -- NULL means the organization declared no default, so a repository genuinely can be unset.
  -- An absent row means nobody has read the schema, which is a third state and not this one.
  default_value TEXT,
  PRIMARY KEY (owner, property_name)
);`

// PropertyDefinition is one property as the organization declares it.
type PropertyDefinition struct {
	Name string
	// HasDefault separates "no default declared" from "the default is the empty string", which
	// are different claims about what a repository gets for doing nothing.
	HasDefault   bool
	DefaultValue string
}

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
		var value any
		if d.HasDefault {
			value = d.DefaultValue
		}
		if _, err := tx.Exec(`
INSERT INTO abom_property_schema (owner, property_name, default_value) VALUES (?, ?, ?)
ON CONFLICT(owner, property_name) DO UPDATE SET default_value = excluded.default_value`,
			owner, d.Name, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// propertyDefault reports the default one key carries, and whether a schema said anything at all.
//
// Across accounts rather than per account, because the breakdown is over every declared account at
// once. Two accounts declaring different defaults for one key leave it unknown: labeling a bucket
// as "nobody chose this" is only honest when it is true everywhere the bucket is counted.
func (s *Store) propertyDefault(name string) (value string, known bool, err error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT default_value FROM abom_property_schema WHERE property_name = ?`, name)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var v *string
		if err := rows.Scan(&v); err != nil {
			return "", false, err
		}
		seen++
		if v == nil {
			// The schema was read and declared no default, so there is nothing to label.
			return "", false, rows.Err()
		}
		value = *v
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	return value, seen == 1, nil
}
