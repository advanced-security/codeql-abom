package store

import (
	"database/sql"
	"fmt"
)

// queryAll runs a query and reads every row into a slice.
//
// Scan stays with the caller; row lifecycle and rows.Err() handling stay here.
//
// A free function rather than a method: Go has no generic methods.
func queryAll[T any](s *Store, what string, scan func(*sql.Rows) (T, error),
	query string, args ...any) ([]T, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	out, err := collectRows(rows, scan)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return out, nil
}

// collectRows releases the cursor before a caller can issue another query on the single connection.
func collectRows[T any](rows *sql.Rows, scan func(*sql.Rows) (T, error)) ([]T, error) {
	defer rows.Close()

	out := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
