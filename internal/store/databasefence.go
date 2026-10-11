package store

import (
	"database/sql"
	"fmt"
	"strings"
)

const publicationLockSQL = `SELECT pg_advisory_xact_lock(
	hashtextextended(current_database() || '.' || current_schema() || '.abom-write', 0))`

// WithLease creates a write-fenced facade; every write revalidates under the same transaction.
func (s *Store) WithLease(lease Lease) (Backend, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := s.validateLease(tx, lease); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.withLease(lease), nil
}

// ActiveLease identifies the coordinator attempt without treating its old expiry as current.
func (s *Store) ActiveLease() (Lease, bool) {
	if s.db.fence == nil {
		return Lease{}, false
	}
	return *s.db.fence, true
}

func (s *Store) withLease(lease Lease) *Store {
	return &Store{
		db:       &database{DB: s.db.DB, postgres: s.db.postgres, fence: &lease, ownerStore: s},
		bulkMode: s.bulkMode, dsn: s.dsn, borrowed: true,
	}
}

func (d *database) guarded(tx *sql.Tx) (*transaction, error) {
	t := &transaction{Tx: tx, postgres: d.postgres}
	if d.postgres {
		if _, err := tx.Exec(publicationLockSQL); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("locking PostgreSQL write publication: %w", err)
		}
	}
	if d.fence != nil {
		if d.ownerStore == nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("fenced database has no lease validator")
		}
		if err := d.ownerStore.validateLease(t, *d.fence); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	return t, nil
}

func writesSQL(query string) (bool, error) {
	t, err := lexSQL(query)
	if err != nil {
		return false, err
	}
	for i, token := range t {
		if i+1 < len(t) && t[i+1].text == "(" {
			continue
		}
		for _, word := range []string{"INSERT", "UPDATE", "DELETE", "REPLACE", "CREATE", "DROP", "ALTER", "TRUNCATE", "VACUUM", "ANALYZE"} {
			if tokenIs(token, word) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (d *database) fencedWrite(query string) (bool, error) {
	if d.fence == nil && !d.readOnly && !d.postgres {
		return false, nil
	}
	write, err := writesSQL(query)
	if err != nil {
		return false, err
	}
	if d.readOnly && write {
		// SQLite's declaration cache is connection-local and remains permitted.
		t, err := lexSQL(query)
		if err == nil && !d.postgres && len(t) >= 3 &&
			(tokenIs(t[0], "CREATE") && tokenIs(t[1], "TEMP") ||
				strings.Contains(query, "temp.declared_edge_")) {
			return false, nil
		}
		return false, fmt.Errorf("writes are unavailable on a read-only backend")
	}
	return (d.fence != nil || d.postgres) && write, nil
}

func commitRow(r *row, tx *transaction) *row {
	r.finish = func(scanErr error) error {
		if scanErr != nil {
			_ = tx.Rollback()
			return scanErr
		}
		return tx.Commit()
	}
	return r
}
