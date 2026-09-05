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
func (s *Store) Migrate() error {
	if err := s.dropStaleDerivedTables(); err != nil {
		return err
	}
	if _, err := s.db.Exec(schemaDDL); err != nil {
		return fmt.Errorf("creating schema: %w", err)
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
