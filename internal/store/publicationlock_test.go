package store

import (
	"strings"
	"testing"
)

func TestWriteTransactionsOwnTheirPublicationLockBeforeTouchingRows(t *testing.T) {
	s := newTestStore(t)
	var other *Store
	var err error
	if s.db.postgres {
		other, err = openPostgres(s.dsn, Options{})
	} else {
		other, err = Open(s.dsn, Options{})
	}
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if s.db.postgres {
		assertPostgresPublicationLock(t, other)
	} else {
		assertSQLitePublicationLock(t, other)
	}
}

func assertPostgresPublicationLock(t *testing.T, other *Store) {
	t.Helper()
	var acquired bool
	if err := other.db.DB.QueryRow(`SELECT pg_try_advisory_xact_lock(
		hashtextextended(current_database() || '.' || current_schema() || '.abom-write', 0))`).
		Scan(&acquired); err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Fatal("another PostgreSQL writer can enter before the first touches a row")
	}
}

func assertSQLitePublicationLock(t *testing.T, other *Store) {
	t.Helper()
	if _, err := other.db.DB.Exec(`PRAGMA busy_timeout = 0`); err != nil {
		t.Fatal(err)
	}
	_, err := other.db.DB.Exec(`INSERT INTO repository(repository_full_name) VALUES ('locked/test')`)
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("another SQLite writer can enter the publication transaction: %v", err)
	}
}
