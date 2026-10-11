package store

import (
	"context"
	"fmt"
	"runtime"
	"sync"
)

// declarationCache keeps what the views read declarations from for as long as the database holds
// what it was read from: LiveCommits and StartingCommits, as the JSON liveDeclared binds, and
// declared_edge for each mode asked for, built into a table of the store's one connection. Reading
// the declarations costs a few hundred milliseconds on the crawled organization, and a page of a
// unit reads them more than once.
//
// Keyed as graph.Models keys the walk's graph: total_changes counts the rows this connection
// changed and data_version moves when any other connection commits, and both restart with a
// connection, so the connection is part of the key. No writer has to announce anything, so a
// newly ingested edge is read by the next request. Building the tables moves neither, so neither
// cache invalidates the other.
type declarationCache struct {
	mu      sync.Mutex
	state   connectionState
	answers string
	read    bool
	tables  map[string]bool
	loads   int
}

type connectionState struct {
	conn        any
	changes     int64
	dataVersion int64
}

// declarationCaches holds one cache per store, dropped when the store is.
var declarationCaches sync.Map

func (s *Store) declarationCache() *declarationCache {
	if c, ok := declarationCaches.Load(s.db); ok {
		return c.(*declarationCache)
	}
	c, loaded := declarationCaches.LoadOrStore(s.db, &declarationCache{})
	if !loaded {
		runtime.AddCleanup(s, func(key any) { declarationCaches.Delete(key) }, any(s.db))
	}
	return c.(*declarationCache)
}

// current forgets what was read for an earlier database state. Called with the lock held.
func (c *declarationCache) current(s *Store) error {
	// Read before loading: a commit landing during the load leaves the key behind what was read,
	// which costs the next request a reload and never serves anything older than the key.
	state, err := s.connectionState()
	if err != nil {
		return err
	}
	if state != c.state {
		c.state, c.read, c.answers, c.tables = state, false, "", map[string]bool{}
	}
	return nil
}

// liveAnswers reads LiveCommits and StartingCommits once for the current state. Called with the
// lock held.
func (c *declarationCache) liveAnswers(s *Store) (string, error) {
	if c.read {
		return c.answers, nil
	}
	live, starting, err := s.liveAndStarting()
	if err != nil {
		return "", err
	}
	answers, err := liveAnswers(live, starting)
	if err != nil {
		return "", err
	}
	c.answers, c.read = answers, true
	c.loads++
	return answers, nil
}

// liveAnswersNow is liveAnswers of LiveCommits as the database stands. It reserves the store's
// one connection, so call it before opening rows.
func (s *Store) liveAnswersNow() (string, error) {
	c := s.declarationCache()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.current(s); err != nil {
		return "", err
	}
	return c.liveAnswers(s)
}

func declarationTableName(mode string) string {
	name := "declared_edge_regular"
	switch mode {
	case AnalysisModeExtended:
		name = "declared_edge_extended"
	case AnalysisModeAnyShape:
		name = "declared_edge_mixed"
	}
	return name
}

func startingNodeTable(mode string) string {
	return "temp." + declarationTableName(mode) + "_starting"
}

func (s *Store) prepareScope(scope, mode string) error {
	if scope != ScopeSupplyChain {
		return nil
	}
	_, err := s.declaredEdgeTable(mode)
	return err
}

// declaredEdgeTable caches a mode's visible declarations and its starting-point membership together.
func (s *Store) declaredEdgeTable(mode string) (string, error) {
	name := declarationTableName(mode)
	table := "temp." + name
	c := s.declarationCache()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.current(s); err != nil {
		return "", err
	}
	if c.tables[name] {
		return table, nil
	}
	answers, err := c.liveAnswers(s)
	if err != nil {
		return "", err
	}
	if s.db.postgres {
		if err := s.postgresDeclarations(name, mode, answers); err != nil {
			return "", err
		}
		c.tables[name] = true
		return table, nil
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`DROP TABLE IF EXISTS ` + table, nil},
		{`CREATE TEMP TABLE ` + name + ` AS WITH ` + declaredEdges(mode) +
			` SELECT * FROM declared_edge`, []any{answers}},
		{`CREATE INDEX ` + table + `_row ON ` + name + `(row_id)`, nil},
		{`CREATE INDEX ` + table + `_src ON ` + name + `(src_node_row_id)`, nil},
		{`CREATE INDEX ` + table + `_dst ON ` + name + `(dst_node_row_id, ref_value)`, nil},
		{`DROP TABLE IF EXISTS ` + startingNodeTable(mode), nil},
		{`CREATE TEMP TABLE ` + name + `_starting AS WITH ` + liveDeclared(mode) + `
		 SELECT DISTINCT d.repository_row_id, d.dst_node_row_id
		   FROM declared d JOIN repository r ON r.row_id = d.repository_row_id
		  WHERE LOWER(r.repository_full_name) NOT IN (SELECT repository FROM live_answer)
		     OR EXISTS (SELECT 1 FROM live_answer a
		                 WHERE a.repository = LOWER(r.repository_full_name) AND a.starting = 1
		                   AND (d.commit_sha = '' OR a.commit_sha = d.commit_sha))`, []any{answers}},
		{`CREATE INDEX ` + startingNodeTable(mode) + `_repo ON ` + name +
			`_starting(repository_row_id, dst_node_row_id)`, nil},
	} {
		if _, err := s.db.Exec(statement.sql, statement.args...); err != nil {
			return "", fmt.Errorf("reading what each line declared at the commits in use: %w", err)
		}
	}
	c.tables[name] = true
	return table, nil
}

func (s *Store) connectionState() (connectionState, error) {
	if s.db.postgres {
		var revision int64
		err := s.db.QueryRow(`SELECT revision FROM abom_store_revision WHERE singleton = 1`).Scan(&revision)
		return connectionState{conn: s.db, changes: revision}, err
	}
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return connectionState{}, fmt.Errorf("reserving a connection to read the database state: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var st connectionState
	if err := conn.Raw(func(driverConn any) error {
		st.conn = driverConn
		return nil
	}); err != nil {
		return connectionState{}, fmt.Errorf("identifying the connection: %w", err)
	}
	if err := conn.QueryRowContext(ctx,
		`SELECT total_changes(), data_version FROM pragma_data_version`).Scan(
		&st.changes, &st.dataVersion); err != nil {
		return connectionState{}, fmt.Errorf("reading the database state: %w", err)
	}
	return st, nil
}
