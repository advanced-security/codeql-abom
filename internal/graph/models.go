package graph

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
)

// Models keeps one loaded graph for the walks that only read it, for as long as the database holds
// what it was loaded from. The zero value is ready to use and safe for concurrent readers.
//
// Keyed by commits rather than by graph generation, because most of what the walk reads changes
// without one: the queue writes resolved commits in place mid-cycle, a declaration promotes a
// repository, a lock is read during analysis, and ingestion lands edges before the cycle records a
// generation. SQLite counts every commit, so no writer has to remember to announce one.
type Models struct {
	// mu is held across a load, so readers arriving together share one rather than each paying.
	mu    sync.Mutex
	state databaseState
	model *model
	loads int
}

// databaseState is what one connection can see. total_changes counts the rows that connection
// inserted, updated or deleted, and data_version moves when any other connection commits, in this
// process or another. Both restart with a connection, so it is part of the key.
type databaseState struct {
	conn        any
	changes     int64
	dataVersion int64
}

// Loads reports how many times the graph has been read from the database.
func (c *Models) Loads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loads
}

func (c *Models) get(b *Builder) (*model, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Read before loading: a commit landing during the load leaves the key behind the graph, which
	// costs the next walk a reload and never serves anything older than the key.
	state, err := stateOf(b.Store.DB())
	if err != nil {
		return nil, err
	}
	if c.model != nil && c.state == state {
		return c.model, nil
	}
	c.model = nil
	m, err := b.load()
	if err != nil {
		return nil, err
	}
	c.model, c.state = m, state
	c.loads++
	return m, nil
}

func stateOf(db *sql.DB) (databaseState, error) {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return databaseState{}, fmt.Errorf("reserving a connection to read the database state: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var s databaseState
	if err := conn.Raw(func(driverConn any) error {
		s.conn = driverConn
		return nil
	}); err != nil {
		return databaseState{}, fmt.Errorf("identifying the connection: %w", err)
	}
	if err := conn.QueryRowContext(ctx,
		`SELECT total_changes(), data_version FROM pragma_data_version`).Scan(
		&s.changes, &s.dataVersion); err != nil {
		return databaseState{}, fmt.Errorf("reading the database state: %w", err)
	}
	return s, nil
}

// walkable is the graph for a walk that only reads it, shared where the builder has Models.
func (b *Builder) walkable() (*model, error) {
	if b.Models == nil {
		return b.load()
	}
	return b.Models.get(b)
}
