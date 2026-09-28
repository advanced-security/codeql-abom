package store

import (
	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// RepositoryScope narrows an answer to the repositories the organization says match.
//
// Empty asks nothing and narrows nothing, which is different from a grouping nobody is in: that
// one narrows to nothing, the way an empty selector already matches nothing.
type RepositoryScope struct {
	Include []viewspec.PropertyMatch
	Exclude []viewspec.PropertyMatch
}

// asked reports whether the scope names anything at all.
func (sc RepositoryScope) asked() bool { return len(sc.Include) > 0 || len(sc.Exclude) > 0 }

// restrict returns a SQL fragment bounding `column` to the scope's repositories, and the arguments
// binding it. The fragment is empty when nothing was asked.
//
// It belongs before an aggregation, never after. A remediation row is an advisory and its
// numbers count repositories, so a restriction applied to finished rows would narrow the list
// while every count still spanned the organization.
// Placed in the WHERE, the counts are recomputed and an advisory that reaches nothing in the
// grouping produces no row at all.
//
// `column` names a repository by full name; callers holding a row id compare through `repository`.
func (s *Store) restrict(sc RepositoryScope, column string) (string, []any, error) {
	clause, args, err := s.restrictClause(sc, column)
	if err != nil || clause == "" {
		return "", args, err
	}
	return " AND " + clause, args, nil
}

// restrictClause is the same restriction as a bare boolean, for a caller assembling its own
// conjunction rather than appending to an existing WHERE.
//
// Derived once and spelled two ways, so the two cannot come to mean different things.
func (s *Store) restrictClause(sc RepositoryScope, column string) (string, []any, error) {
	if !sc.asked() {
		return "", nil, nil
	}
	names, err := s.SourceRepositoriesFor(sc.Include, sc.Exclude)
	if err != nil {
		return "", nil, err
	}
	if len(names) == 0 {
		// A grouping nobody is in answers nothing. Falling through to no restriction would report
		// the whole organization under the name of a part of it.
		return "0", nil, nil
	}
	list, args := inList(names)
	return "LOWER(" + column + ") IN " + list, args, nil
}
