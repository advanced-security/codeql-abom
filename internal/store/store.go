// Package store persists the ABOM graph and its supporting vulnerability data in SQLite.
//
// The design target is an organization of 20,000+ repositories, which implies millions of edge
// rows. Two choices follow from that and are load-bearing rather than incidental: indexes are
// built after bulk loading rather than maintained during it, and writes are batched through
// prepared statements inside explicit transactions.
package store

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// Store owns the database handle and the schema lifecycle.
type Store struct {
	db       *sql.DB
	bulkMode bool
}

// Options tunes how the database is opened.
type Options struct {
	// BulkLoad trades crash durability for insert throughput. Safe for ingestion runs, which
	// are restartable from the ingest ledger, and not safe for a long-lived serving database.
	BulkLoad bool

	// CacheSizeKB is the page cache size. Negative values are interpreted by SQLite as KiB.
	CacheSizeKB int
}

// DefaultCacheSizeKB gives SQLite a 256 MiB page cache, which keeps the b-tree interior pages
// resident during multi-million row loads.
const DefaultCacheSizeKB = 256 * 1024

// Open opens (creating if needed) the database at path and applies connection tuning.
func Open(path string, opts Options) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=30000&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	// A single writer avoids SQLITE_BUSY contention; concurrency belongs in the work queue
	// feeding this store, not in competing writers.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, bulkMode: opts.BulkLoad}
	if err := s.applyPragmas(opts); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) applyPragmas(opts Options) error {
	cache := opts.CacheSizeKB
	if cache == 0 {
		cache = DefaultCacheSizeKB
	}

	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA temp_store = MEMORY",
		fmt.Sprintf("PRAGMA cache_size = -%d", cache),
	}
	if s.bulkMode {
		pragmas = append(pragmas, "PRAGMA synchronous = OFF")
	} else {
		pragmas = append(pragmas, "PRAGMA synchronous = NORMAL")
	}

	for _, p := range pragmas {
		if _, err := s.db.Exec(p); err != nil {
			return fmt.Errorf("applying %q: %w", p, err)
		}
	}
	return nil
}

// DB exposes the handle for read paths and analytics that need raw SQL.
func (s *Store) DB() *sql.DB { return s.db }

// Migrate creates all tables and adds any columns introduced since the database was created.
// It is safe to call on an existing database.
// createTables are the CREATE TABLE statements that follow the main schema, in the order they run.
//
// Every one runs before the column pass, because that pass is one sweep of ALTER TABLE and a table
// it names but that does not exist yet fails on every fresh database. Additive, so a corpus
// upgraded in place gains them without a rebuild.
var createTables = []struct {
	what string
	ddl  string
}{
	{"the commit content table", commitContentDDL},
	{"the lock coverage table", lockCoverageDDL},
	{"the declared scope tables", ownedScopeDDL},
	{"the cycle spend table", cycleSpendDDL},
	{"the saved view table", savedViewDDL},
	{"the response cache table", httpCacheDDL},
	{"the discovery state table", discoveryStateDDL},
	{"the enumeration state table", enumerationStateDDL},
	{"the repository property table", repositoryPropertyDDL},
	{"the property schema table", propertySchemaDDL},
	{"the policy tables", policyDDL},
}

func (s *Store) Migrate() error {
	if err := s.dropStaleDerivedTables(); err != nil {
		return err
	}
	if _, err := s.db.Exec(schemaDDL); err != nil {
		return fmt.Errorf("creating schema: %w", err)
	}
	for _, t := range createTables {
		if _, err := s.db.Exec(t.ddl); err != nil {
			return fmt.Errorf("creating %s: %w", t.what, err)
		}
	}
	for _, c := range addedColumns {
		has, err := s.hasColumn(c.table, c.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.column, c.decl)
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("adding %s.%s: %w", c.table, c.column, err)
		}
	}
	// After the additive pass, so a column this drops can never be one that pass just added.
	for _, c := range droppedColumns {
		has, err := s.hasColumn(c.table, c.column)
		if err != nil {
			return err
		}
		if !has {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", c.table, c.column)
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("dropping %s.%s: %w", c.table, c.column, err)
		}
	}
	// After the columns exist, because the rebuild copies every one of them forward.
	if err := s.migrateFrontierRefKey(); err != nil {
		return err
	}
	if err := s.migrateObservationFactKey(); err != nil {
		return err
	}
	// The one index a read-only server cannot wait for CreateIndexes to build.
	if _, err := s.db.Exec(queryRunIndexDDL); err != nil {
		return fmt.Errorf("indexing run identity: %w", err)
	}
	// The observation table's other reader asks by attribute name, which the fact key cannot
	// serve. Here rather than in indexDDL because that only runs after a bulk load, so an index
	// added there never reaches a database that already exists.
	if _, err := s.db.Exec(observationIndexDDL); err != nil {
		return fmt.Errorf("indexing observations by name: %w", err)
	}
	// And the rest of them, for the same reason, once there is no bulk load to slow down.
	// `CreateIndexes` is called by the seed build alone, so every other database — one being
	// served, expanded or read by the CLI — carried whatever indexes existed when it was first
	// built and none added since. Measured 2026-09-15: 20 of 22 were missing from the reference
	// corpus, and the absent `idx_edge_dst` alone made the Inventory scan all 14,196 edges once
	// per row. Rebuilding an index that exists costs a catalog lookup.
	if !s.bulkMode {
		if err := s.CreateIndexes(); err != nil {
			return err
		}
	}
	// After the columns, which the views select on.
	if _, err := s.db.Exec(liveViewDDL); err != nil {
		return fmt.Errorf("creating the live views: %w", err)
	}
	// Selects on abom_account.scopes, which the column pass above may have just added.
	if _, err := s.db.Exec(honoredScopeDDL); err != nil {
		return fmt.Errorf("creating the honored scope view: %w", err)
	}
	// Before seeding, so a binding that only needs renaming still counts as configured and the
	// defaults are not written over it.
	if err := s.migrateSuiteNames(); err != nil {
		return err
	}
	return s.seedTierSuites()
}

// dropStaleDerivedTables removes derived tables that no longer have a consumer.
//
// Columns are normally only added, because dropping one loses data. This table held nothing
// that is not recomputed from the findings themselves, so it is discarded rather than migrated.
func (s *Store) dropStaleDerivedTables() error {
	// abom_pinning cached findings per rule alongside a denominator, and both were a liability:
	// the denominator related two unconnected quantities, and the cache could fall behind the
	// findings it summarized. Callers now count from abom_finding directly.
	if _, err := s.db.Exec(`DROP TABLE IF EXISTS abom_pinning`); err != nil {
		return fmt.Errorf("dropping abom_pinning: %w", err)
	}
	// abom_owned_org held accounts the operator typed in. A declaration cannot be told apart from
	// a guess, yet it decided which findings were reported as theirs to fix, so those rows are
	// discarded rather than carried into abom_account: they were never verified and there is no
	// way to verify them retroactively.
	if _, err := s.db.Exec(`DROP TABLE IF EXISTS abom_owned_org`); err != nil {
		return fmt.Errorf("dropping abom_owned_org: %w", err)
	}
	return nil
}

func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, fmt.Errorf("inspecting %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// CreateIndexes builds every index. Call once after bulk loading has finished.
func (s *Store) CreateIndexes() error {
	if _, err := s.db.Exec(indexDDL); err != nil {
		return fmt.Errorf("creating indexes: %w", err)
	}
	return nil
}

// Optimize updates the query planner's statistics and compacts the file.
//
// VACUUM rewrites the database, so it must run after indexes exist, not before.
func (s *Store) Optimize() error {
	for _, stmt := range []string{"ANALYZE", "VACUUM"} {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("running %s: %w", stmt, err)
		}
	}
	return nil
}

// Close flushes the write-ahead log back into the main database file before closing, so the
// resulting file is self-contained and safe to compress and ship.
func (s *Store) Close() error {
	if _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		// The checkpoint failure is the one worth reporting; closing is best effort after it.
		_ = s.db.Close()
		return fmt.Errorf("checkpointing wal: %w", err)
	}
	return s.db.Close()
}
