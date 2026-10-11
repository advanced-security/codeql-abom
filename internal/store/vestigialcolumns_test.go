package store

import (
	"path/filepath"
	"testing"
)

// The three columns a `gh mrva` era left behind. They are named here rather than derived from the
// schema so that this test and the migration can disagree.
var vestigialRepositoryColumns = []string{"result_count", "artifact_size_in_bytes", "analysis_id"}

// Dropping a column must not take the row with it.
//
// Every other migration in this store is additive, which is safe by construction. This one is not,
// and a `DROP COLUMN` that SQLite implements as a table rewrite can lose data no fresh-database
// test would miss.
func TestSQLiteDroppingVestigialColumnsKeepsEveryRepositoryRow(t *testing.T) {
	s := migratedLegacyStoreWith(t, func(s *Store) {
		if _, err := s.db.Exec(`
			CREATE TABLE repository (
			  row_id                 INTEGER PRIMARY KEY,
			  repository_full_name   TEXT NOT NULL UNIQUE,
			  repository_url         TEXT,
			  analysis_status        TEXT,
			  result_count           INTEGER,
			  artifact_size_in_bytes INTEGER,
			  analysis_id            TEXT
			);
			INSERT INTO repository
			  (repository_full_name, repository_url, analysis_status, result_count,
			   artifact_size_in_bytes, analysis_id)
			VALUES
			  ('octo/kept', 'https://github.com/octo/kept', 'complete', 7, 1024, 'a-1'),
			  ('octo/also-kept', NULL, NULL, NULL, NULL, NULL)`); err != nil {
			t.Fatalf("seeding the legacy repository table: %v", err)
		}
	})

	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM repository`).Scan(&rows); err != nil {
		t.Fatalf("counting repositories: %v", err)
	}
	if rows != 2 {
		t.Errorf("repository holds %d rows, want the 2 that were there before the migration", rows)
	}

	// The surviving row keeps the columns that were never vestigial.
	var url, status string
	if err := s.db.QueryRow(`
		SELECT COALESCE(repository_url, ''), COALESCE(analysis_status, '')
		FROM repository WHERE repository_full_name = 'octo/kept'`).Scan(&url, &status); err != nil {
		t.Fatalf("reading the kept row: %v", err)
	}
	if url != "https://github.com/octo/kept" || status != "complete" {
		t.Errorf("row = (%q, %q), want its url and status intact", url, status)
	}

	for _, column := range vestigialRepositoryColumns {
		has, err := s.hasColumn("repository", column)
		if err != nil {
			t.Fatalf("hasColumn(%s): %v", column, err)
		}
		if has {
			t.Errorf("repository still has %s, which nothing reads or writes", column)
		}
	}
}

// A database created today must not gain the columns back through the CREATE TABLE.
func TestAFreshDatabaseHasNoVestigialRepositoryColumns(t *testing.T) {
	s, err := openTestStore(t, filepath.Join(t.TempDir(), "abom.db"), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, column := range vestigialRepositoryColumns {
		has, err := s.hasColumn("repository", column)
		if err != nil {
			t.Fatalf("hasColumn(%s): %v", column, err)
		}
		if has {
			t.Errorf("a fresh database has %s, so the CREATE TABLE still declares it", column)
		}
	}
}
