package store

import (
	"database/sql"
	"testing"
)

// A column in `addedColumns` and not in CREATE TABLE means a fresh database and an upgraded one
// are different shapes, and every read works against one of them by luck.
//
// The opposite direction — a column in CREATE TABLE that `addedColumns` forgot — is the defect
// that actually happened: `result_path` was declared, shipped, and answered 404 on the working
// corpus because `Migrate` could not add it to a table that already existed. It is deliberately
// **not** guarded here, and a round trip that drops each column and re-migrates was written and
// thrown away: it cannot tell a column added last week from one present since the first build,
// so it demanded `addedColumns` cover all 200-odd of them. Knowing which columns postdate their
// table needs a schema-version history this store does not keep. Until it does, the check is a
// reviewer asking "has this table shipped?" — and the cost of missing it is one 404 on a database
// nobody rebuilt.
func TestEveryMigratedColumnIsAlsoDeclared(t *testing.T) {
	fresh := newTestStore(t)
	for _, added := range addedColumns {
		if !hasColumn(t, fresh.DB(), added.table, added.column) {
			t.Errorf("%s.%s is in addedColumns but not in CREATE TABLE, so a fresh database "+
				"gets it only by migration and the two shapes differ", added.table, added.column)
		}
	}
}

func hasColumn(t *testing.T, db *sql.DB, table, column string) bool {
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
