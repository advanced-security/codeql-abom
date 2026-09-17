package store

import (
	"path/filepath"
	"regexp"
	"testing"
)

var indexDeclaration = regexp.MustCompile(`CREATE INDEX IF NOT EXISTS (\w+)`)

// declaredIn reads the index names out of a DDL rather than restating them, so an index added to
// the schema is covered here without anyone remembering to add it.
func declaredIn(ddl ...string) []string {
	var out []string
	for _, d := range ddl {
		for _, m := range indexDeclaration.FindAllStringSubmatch(d, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

func presentIndexes(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.DB().Query(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND sql IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func migrated(t *testing.T, opts Options) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// An index declared in the DDL and absent from the database is not an index.
//
// `CreateIndexes` runs once after a bulk load, and only the seed build calls it, so an index added
// to `indexDDL` never reached a database that already existed. Measured 2026-09-15 on the crawled
// organization: 20 of 22 declared indexes were missing, including `idx_edge_dst`, which is why
// every per-node reference lookup scanned all 14,196 edges and the Inventory took 2.7 s. Two
// indexes had already been special-cased onto the migrate path, each with a comment naming this
// same cause; this asserts it for all of them instead.
func TestMigrateBuildsEveryDeclaredIndexWhenNotBulkLoading(t *testing.T) {
	s := migrated(t, Options{})
	present := presentIndexes(t, s)

	declared := declaredIn(schemaDDL, indexDDL, queryRunIndexDDL, observationIndexDDL)
	if len(declared) < 20 {
		t.Fatalf("only %d indexes declared; the DDL is not being read", len(declared))
	}
	for _, name := range declared {
		if !present[name] {
			t.Errorf("%s is declared in the DDL and absent from the database", name)
		}
	}
}

// Ingest still defers them, because an index is paid for on every insert during a bulk load.
func TestABulkLoadDefersIndexesUntilItIsFinished(t *testing.T) {
	s := migrated(t, Options{BulkLoad: true})

	// Two are built unconditionally, because a server may read the database while an ingest run
	// is still loading it. Everything else waits.
	always := map[string]bool{}
	for _, name := range declaredIn(queryRunIndexDDL, observationIndexDDL) {
		always[name] = true
	}
	var deferred []string
	for _, name := range declaredIn(indexDDL) {
		if !always[name] {
			deferred = append(deferred, name)
		}
	}
	if len(deferred) == 0 {
		t.Fatal("nothing is deferred, so this asserts nothing")
	}

	present := presentIndexes(t, s)
	for _, name := range deferred {
		if present[name] {
			t.Errorf("a bulk load built %s up front", name)
		}
	}

	if err := s.CreateIndexes(); err != nil {
		t.Fatalf("CreateIndexes: %v", err)
	}
	present = presentIndexes(t, s)
	for _, name := range deferred {
		if !present[name] {
			t.Errorf("%s is missing after CreateIndexes", name)
		}
	}
}
