package store

import (
	"database/sql"
	"fmt"
	"net/url"

	_ "github.com/lib/pq"
)

func openPostgres(dsn string, opts Options) (*Store, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("opening PostgreSQL: invalid connection URL")
	}
	openDSN := dsn
	if opts.ReadOnly {
		q := u.Query()
		q.Set("default_transaction_read_only", "on")
		u.RawQuery = q.Encode()
		openDSN = u.String()
	}
	db, err := sql.Open("postgres", openDSN)
	if err != nil {
		return nil, fmt.Errorf("opening PostgreSQL: %w", err)
	}
	// Temporary declaration tables are connection-local. Multi-writer scheduling is separate.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connecting to PostgreSQL: %w", err)
	}
	s := &Store{db: &database{DB: db, postgres: true, readOnly: opts.ReadOnly}, bulkMode: opts.BulkLoad, dsn: dsn}
	if err := s.refuseDemo(opts); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}
