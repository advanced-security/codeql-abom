package store

import (
	"fmt"
	"slices"
	"strings"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// PolicyScopeMigration is what writing retired scopes into their predicates did.
type PolicyScopeMigration struct {
	Migrated []string `json:"migrated"`
	// Left names each policy that could not be migrated, and why. The evaluation refuses them.
	Left map[string]string `json:"left"`
}

// MigratePolicyScopes writes each retired unit or repository scope into its policy's predicate,
// as the term the predicate would say it with, and keeps the policy's adoption.
//
// The same question reworded is not an edit: a unit scope and `properties=<key>:<unit>` select the
// same repositories, which the tests hold, so a policy adopted against one is adopted against the
// other. `unitProperty` is the configured grouping key, and without it a unit scope stays.
func (s *Store) MigratePolicyScopes(unitProperty string) (PolicyScopeMigration, error) {
	out := PolicyScopeMigration{Migrated: []string{}, Left: map[string]string{}}
	rows, err := s.db.Query(`
SELECT name, predicate, scope_kind, COALESCE(scope_value, '') FROM abom_policy
 WHERE scope_kind <> ? ORDER BY name`, policyScopeOrganization)
	if err != nil {
		return out, err
	}
	type legacy struct{ name, predicate, kind, value string }
	var pending []legacy
	for rows.Next() {
		var l legacy
		if err := rows.Scan(&l.name, &l.predicate, &l.kind, &l.value); err != nil {
			rows.Close()
			return out, err
		}
		pending = append(pending, l)
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	for _, l := range pending {
		predicate, err := scopedPredicate(l.predicate, l.kind, l.value, unitProperty)
		if err != nil {
			out.Left[l.name] = err.Error()
			continue
		}
		if _, err := s.db.Exec(`
UPDATE abom_policy SET predicate = ?, scope_kind = ?, scope_value = '' WHERE name = ?`,
			predicate, policyScopeOrganization, l.name); err != nil {
			return out, err
		}
		out.Migrated = append(out.Migrated, l.name)
	}
	return out, nil
}

// scopedPredicate is a predicate with a retired scope written into it, intersected with whatever
// the predicate already said about the same thing.
func scopedPredicate(predicate, kind, value, unitProperty string) (string, error) {
	spec, err := viewspec.Parse(predicate)
	if err != nil {
		return "", err
	}
	switch kind {
	case policyScopeRepository:
		if len(spec.Repositories) > 0 &&
			!slices.ContainsFunc(spec.Repositories, func(r string) bool { return strings.EqualFold(r, value) }) {
			return "", fmt.Errorf("its predicate names no repository %s is bound to, %s", kind, value)
		}
		spec.Repositories = []string{value}

	case policyScopeUnit:
		if strings.TrimSpace(unitProperty) == "" {
			return "", fmt.Errorf("a unit scope needs the property it divides by, and none is configured")
		}
		values, others := []string{}, []viewspec.PropertyMatch{}
		for _, m := range spec.Properties {
			if m.Name == unitProperty {
				values = append(values, m.Value)
			} else {
				others = append(others, m)
			}
		}
		if len(values) > 0 && !slices.Contains(values, value) {
			return "", fmt.Errorf("its predicate names no %s value %s is bound to, %s",
				unitProperty, kind, value)
		}
		spec.Properties = append(others, viewspec.PropertyMatch{Name: unitProperty, Value: value})

	default:
		return "", fmt.Errorf("%q is not a scope this build knows", kind)
	}
	return spec.String(), nil
}
