package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lib/pq"
)

// SnapshotStore returns an independent writable snapshot. Cleanup closes it and removes only
// the file directory or PostgreSQL schema this call created; callers must not close it separately.
// SQLite uses dir for scratch files; PostgreSQL creates no files and ignores the local directory.
func (s *Store) SnapshotStore(dir string) (Backend, func() error, error) {
	if s.db.postgres {
		return s.postgresSnapshot()
	}
	dir, err := os.MkdirTemp(dir, "abom-snapshot-")
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, "abom.db")
	if err := s.SnapshotInto(path); err != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, err
	}
	snapshot, err := Open(path, Options{})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, err
	}
	var once sync.Once
	var cleanupErr error
	cleanup := func() error {
		once.Do(func() {
			cleanupErr = errors.Join(snapshot.Close(), os.RemoveAll(dir))
		})
		return cleanupErr
	}
	return snapshot, cleanup, nil
}

func (s *Store) postgresSnapshot() (Backend, func() error, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, nil, err
	}
	schema := "abom_snapshot_" + hex.EncodeToString(id)
	admin, err := openPostgres(s.dsn, Options{})
	if err != nil {
		return nil, nil, err
	}
	if _, err := admin.db.DB.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		_ = admin.Close()
		return nil, nil, fmt.Errorf("creating a PostgreSQL snapshot requires CREATE SCHEMA permission: %w", err)
	}
	var snapshot *Store
	var once sync.Once
	var cleanupErr error
	cleanup := func() error {
		once.Do(func() {
			if snapshot != nil {
				cleanupErr = snapshot.Close()
			}
			_, dropErr := admin.db.DB.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
			cleanupErr = errors.Join(cleanupErr, dropErr, admin.Close())
		})
		return cleanupErr
	}
	fail := func(err error) (Backend, func() error, error) {
		return nil, nil, errors.Join(err, cleanup())
	}
	u, err := url.Parse(s.dsn)
	if err != nil {
		return fail(fmt.Errorf("parsing the PostgreSQL snapshot connection"))
	}
	params := u.Query()
	params.Set("search_path", schema)
	u.RawQuery = params.Encode()
	snapshot, err = openPostgres(u.String(), Options{})
	if err != nil {
		return fail(err)
	}
	if err := snapshot.Migrate(); err != nil {
		return fail(err)
	}
	if err := s.copyPostgresSnapshot(snapshot, schema); err != nil {
		return fail(err)
	}
	return snapshot, cleanup, nil
}

type snapshotConstraint struct {
	table, name          string
	deferrable, deferred bool
}

func (s *Store) copyPostgresSnapshot(snapshot *Store, target string) error {
	// Deferral lets rows retain their original IDs even when foreign keys form cycles.
	rows, err := snapshot.db.DB.Query(`SELECT r.relname, c.conname, c.condeferrable, c.condeferred
		FROM pg_constraint c JOIN pg_class r ON r.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = r.relnamespace
		WHERE c.contype = 'f' AND n.nspname = current_schema()`)
	if err != nil {
		return err
	}
	var constraints []snapshotConstraint
	for rows.Next() {
		var c snapshotConstraint
		if err := rows.Scan(&c.table, &c.name, &c.deferrable, &c.deferred); err != nil {
			_ = rows.Close()
			return err
		}
		constraints = append(constraints, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, c := range constraints {
		if _, err := snapshot.db.DB.Exec(`ALTER TABLE ` + pq.QuoteIdentifier(c.table) +
			` ALTER CONSTRAINT ` + pq.QuoteIdentifier(c.name) + ` DEFERRABLE INITIALLY DEFERRED`); err != nil {
			return err
		}
	}
	tx, err := s.db.DB.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var source string
	if err := tx.QueryRow(`SELECT current_schema()`).Scan(&source); err != nil {
		return err
	}
	tables, err := postgresSnapshotTables(tx, source)
	if err != nil {
		return err
	}
	var targets []string
	for _, table := range tables {
		targets = append(targets, pq.QuoteIdentifier(target)+"."+pq.QuoteIdentifier(table))
	}
	if len(targets) == 0 {
		return fmt.Errorf("PostgreSQL source has no store tables to snapshot")
	}
	// Copy the authoritative change log too; replaying triggers would invent new changes.
	for _, table := range targets {
		if _, err := tx.Exec(`ALTER TABLE ` + table + ` DISABLE TRIGGER USER`); err != nil {
			return fmt.Errorf("suspending snapshot-local derived writes: %w", err)
		}
	}
	if _, err := tx.Exec(`TRUNCATE ` + strings.Join(targets, ", ")); err != nil {
		return fmt.Errorf("preparing empty PostgreSQL snapshot tables: %w", err)
	}
	for i, table := range tables {
		columns, identity, err := postgresSnapshotColumns(tx, source, table)
		if err != nil {
			return err
		}
		from := pq.QuoteIdentifier(source) + "." + pq.QuoteIdentifier(table)
		if _, err := tx.Exec(`INSERT INTO ` + targets[i] + ` (` + columns + `) OVERRIDING SYSTEM VALUE SELECT ` +
			columns + ` FROM ` + from); err != nil {
			return fmt.Errorf("copying PostgreSQL snapshot table %s: %w", table, err)
		}
		for _, column := range identity {
			q := pq.QuoteIdentifier(column)
			if _, err := tx.Exec(`SELECT setval(pg_get_serial_sequence($1, $2),
				COALESCE(MAX(`+q+`), 1), MAX(`+q+`) IS NOT NULL) FROM `+targets[i],
				targets[i], column); err != nil {
				return fmt.Errorf("positioning snapshot identity %s.%s: %w", table, column, err)
			}
		}
	}
	if _, err := tx.Exec(`SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		return fmt.Errorf("validating PostgreSQL snapshot references: %w", err)
	}
	for _, table := range targets {
		if _, err := tx.Exec(`ALTER TABLE ` + table + ` ENABLE TRIGGER USER`); err != nil {
			return fmt.Errorf("restoring snapshot-local derived writes: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing PostgreSQL snapshot: %w", err)
	}
	for _, c := range constraints {
		mode := "NOT DEFERRABLE"
		if c.deferrable {
			mode = "DEFERRABLE INITIALLY IMMEDIATE"
			if c.deferred {
				mode = "DEFERRABLE INITIALLY DEFERRED"
			}
		}
		if _, err := snapshot.db.DB.Exec(`ALTER TABLE ` + pq.QuoteIdentifier(c.table) +
			` ALTER CONSTRAINT ` + pq.QuoteIdentifier(c.name) + ` ` + mode); err != nil {
			return err
		}
	}
	return nil
}

func postgresSnapshotTables(tx *sql.Tx, schema string) ([]string, error) {
	rows, err := tx.Query(`SELECT tablename FROM pg_tables WHERE schemaname = $1
		AND (tablename = 'repository' OR starts_with(tablename, 'abom_'))
		ORDER BY tablename`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

func postgresSnapshotColumns(tx *sql.Tx, schema, table string) (string, []string, error) {
	rows, err := tx.Query(`SELECT column_name, is_identity FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var columns, identity []string
	for rows.Next() {
		var name, generated string
		if err := rows.Scan(&name, &generated); err != nil {
			return "", nil, err
		}
		columns = append(columns, pq.QuoteIdentifier(name))
		if generated == "YES" {
			identity = append(identity, name)
		}
	}
	return strings.Join(columns, ", "), identity, rows.Err()
}
