package store

import (
	"database/sql"
	"testing"
)

// Every migrated column must also be present in the CREATE TABLE shape for fresh databases.
func TestEveryMigratedColumnIsAlsoDeclared(t *testing.T) {
	fresh := newTestStore(t)
	for _, added := range addedColumns {
		if fresh.db.postgres {
			has, err := fresh.hasColumn(added.table, added.column)
			if err != nil || !has {
				t.Errorf("PostgreSQL lacks %s.%s: %v", added.table, added.column, err)
			}
			continue
		}
		if !hasColumn(t, fresh.DB(), added.table, added.column) {
			t.Errorf("%s.%s is in addedColumns but not in CREATE TABLE, so a fresh database "+
				"gets it only by migration and the two shapes differ", added.table, added.column)
		}
	}
}

func hasColumn(t *testing.T, db interface {
	Query(string, ...any) (*sql.Rows, error)
}, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("reading %s: %v", table, err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scanning a column of %s: %v", table, err)
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the columns of %s: %v", table, err)
	}
	return found
}
