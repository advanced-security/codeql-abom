package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// The adapter stays inside store: callers receive domain operations, never a driver.
type database struct {
	*sql.DB
	postgres   bool
	relationMu sync.RWMutex
	relations  map[string]string
	fence      *Lease
	ownerStore *Store
	readOnly   bool
}

type transaction struct {
	*sql.Tx
	postgres bool
}

type connection struct {
	*sql.Conn
	postgres bool
	database *database
}

type statement struct {
	*sql.Stmt
	postgres bool
	identity bool
	bindings []int
}

type row struct {
	value  *sql.Row
	err    error
	finish func(error) error
}

func (r *row) Scan(dest ...any) error {
	err := r.err
	if err == nil {
		err = r.value.Scan(dest...)
	}
	if r.finish != nil {
		return r.finish(err)
	}
	return err
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	PrepareContext(context.Context, string) (*sql.Stmt, error)
}

func dbArgs(postgres bool, args []any) []any {
	if !postgres {
		return args
	}
	out := append([]any(nil), args...)
	for i, a := range out {
		if b, ok := a.(bool); ok {
			out[i] = 0
			if b {
				out[i] = 1
			}
		}
	}
	return out
}

func querySQL(pg bool, query string) (string, error) {
	if !pg {
		return query, nil
	}
	tokens, err := lexSQL(query)
	if err != nil {
		return "", err
	}
	if startsWithSQL(tokens, "CREATE", "TABLE") {
		return postgresTableDDL(query)
	}
	return postgresSQL(query)
}

// SQLite numbered slots may be unused. PostgreSQL must receive only parameters SQL references.
func compactParameters(query string) (string, []int, error) {
	tokens, err := lexSQL(query)
	if err != nil {
		return "", nil, err
	}
	positions := map[int]int{}
	var bindings []int
	for i := range tokens {
		if tokens[i].kind != 'p' || !strings.HasPrefix(tokens[i].text, "$") {
			continue
		}
		slot, err := strconv.Atoi(tokens[i].text[1:])
		if err != nil || slot < 1 {
			return "", nil, fmt.Errorf("invalid PostgreSQL parameter")
		}
		position, ok := positions[slot]
		if !ok {
			bindings = append(bindings, slot-1)
			position = len(bindings)
			positions[slot] = position
		}
		tokens[i].text = "$" + strconv.Itoa(position)
	}
	return renderSQL(tokens), bindings, nil
}

func boundArgs(pg bool, bindings []int, args []any) ([]any, error) {
	if !pg {
		return args, nil
	}
	expected := 0
	for _, slot := range bindings {
		expected = max(expected, slot+1)
	}
	if len(args) != expected {
		return nil, fmt.Errorf("SQL expects %d argument slots, got %d", expected, len(args))
	}
	out := make([]any, len(bindings))
	for i, slot := range bindings {
		if slot >= len(args) {
			return nil, fmt.Errorf("SQL parameter %d has no argument", slot+1)
		}
		out[i] = args[slot]
	}
	return dbArgs(true, out), nil
}

func executionQuery(pg bool, query string) (string, []int, error) {
	q, err := querySQL(pg, query)
	if err != nil || !pg {
		return q, nil, err
	}
	return compactParameters(q)
}

func queryRow(ctx context.Context, db sqlExecutor, pg bool, query string, args ...any) *row {
	q, bindings, err := executionQuery(pg, query)
	if err != nil {
		return &row{err: err}
	}
	args, err = boundArgs(pg, bindings, args)
	if err != nil {
		return &row{err: err}
	}
	return &row{value: db.QueryRowContext(ctx, q, args...)}
}

func queryRows(ctx context.Context, db sqlExecutor, pg bool, query string, args ...any) (*sql.Rows, error) {
	q, bindings, err := executionQuery(pg, query)
	if err != nil {
		return nil, err
	}
	args, err = boundArgs(pg, bindings, args)
	if err != nil {
		return nil, err
	}
	return db.QueryContext(ctx, q, args...)
}

type insertResult struct{ id, count int64 }

func (r insertResult) LastInsertId() (int64, error) { return r.id, nil }
func (r insertResult) RowsAffected() (int64, error) { return r.count, nil }

func insertedRows(rows *sql.Rows, err error) (sql.Result, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result insertResult
	for rows.Next() {
		if err := rows.Scan(&result.id); err != nil {
			return nil, err
		}
		result.count++
	}
	return result, rows.Err()
}

// Only plain inserts whose callers request an identity use RETURNING. Upserts keep lookups.
func insertIdentity(q string) string {
	t, err := lexSQL(q)
	if err != nil || len(t) < 4 || !tokenIs(t[0], "INSERT") || !tokenIs(t[1], "INTO") {
		return ""
	}
	for _, v := range t {
		if tokenIs(v, "RETURNING") || tokenIs(v, "CONFLICT") || v.text == ";" {
			return ""
		}
	}
	switch strings.ToLower(t[2].text) {
	case "abom_graph_generation":
		return "generation"
	case "abom_authoring_session", "abom_authoring_message", "abom_request_draft",
		"abom_request_draft_event", "abom_authoring_review", "abom_query_request",
		"abom_comparison", "abom_setup_write", "abom_setup_write_member",
		"abom_authoring_ast", "abom_analysis_run", "abom_authoring_event",
		"abom_policy_simulation", "abom_setup_preview", "abom_setup_batch", "abom_finding", "abom_query_run":
		return "row_id"
	case "abom_owned_property_clause":
		return "id"
	}
	return ""
}

func execSQL(ctx context.Context, db sqlExecutor, pg bool, query string, args ...any) (sql.Result, error) {
	q, bindings, err := executionQuery(pg, query)
	if err != nil {
		return nil, err
	}
	args, err = boundArgs(pg, bindings, args)
	if err != nil {
		return nil, err
	}
	if pg {
		if id := insertIdentity(q); id != "" {
			return insertedRows(db.QueryContext(ctx, q+" RETURNING "+id, args...))
		}
	}
	return db.ExecContext(ctx, q, args...)
}

func prepareSQLContext(ctx context.Context, db sqlExecutor, pg bool, query string) (*statement, error) {
	q, bindings, err := executionQuery(pg, query)
	if err != nil {
		return nil, err
	}
	id := ""
	if pg {
		id = insertIdentity(q)
		if id != "" {
			q += " RETURNING " + id
		}
	}
	s, err := db.PrepareContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("preparing SQL: %w", err)
	}
	return &statement{Stmt: s, postgres: pg, identity: id != "", bindings: bindings}, nil
}

func (s *statement) Exec(args ...any) (sql.Result, error) {
	return s.ExecContext(context.Background(), args...)
}
func (s *statement) ExecContext(ctx context.Context, args ...any) (sql.Result, error) {
	args, err := boundArgs(s.postgres, s.bindings, args)
	if err != nil {
		return nil, err
	}
	if s.identity {
		return insertedRows(s.Stmt.QueryContext(ctx, args...))
	}
	return s.Stmt.ExecContext(ctx, args...)
}
func (s *statement) Query(args ...any) (*sql.Rows, error) {
	return s.QueryContext(context.Background(), args...)
}
func (s *statement) QueryContext(ctx context.Context, args ...any) (*sql.Rows, error) {
	args, err := boundArgs(s.postgres, s.bindings, args)
	if err != nil {
		return nil, err
	}
	return s.Stmt.QueryContext(ctx, args...)
}
func (s *statement) QueryRow(args ...any) *row {
	return s.QueryRowContext(context.Background(), args...)
}
func (s *statement) QueryRowContext(ctx context.Context, args ...any) *row {
	args, err := boundArgs(s.postgres, s.bindings, args)
	if err != nil {
		return &row{err: err}
	}
	return &row{value: s.Stmt.QueryRowContext(ctx, args...)}
}

func (d *database) Exec(q string, a ...any) (sql.Result, error) {
	return d.ExecContext(context.Background(), q, a...)
}
func (d *database) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	fenced, err := d.fencedWrite(q)
	if err != nil {
		return nil, err
	}
	if !fenced {
		return execSQL(ctx, d.DB, d.postgres, q, a...)
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := execSQL(ctx, tx.Tx, d.postgres, q, a...)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}
func (d *database) Query(q string, a ...any) (*sql.Rows, error) {
	return d.QueryContext(context.Background(), q, a...)
}
func (d *database) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	if fenced, err := d.fencedWrite(q); err != nil || fenced {
		if err == nil {
			err = fmt.Errorf("a leased multi-row write requires an explicit transaction")
		}
		return nil, err
	}
	var err error
	q, err = d.expandRelations(q)
	if err != nil {
		return nil, err
	}
	return queryRows(ctx, d.DB, d.postgres, q, a...)
}
func (d *database) QueryRow(q string, a ...any) *row {
	return d.QueryRowContext(context.Background(), q, a...)
}
func (d *database) QueryRowContext(ctx context.Context, q string, a ...any) *row {
	var err error
	q, err = d.expandRelations(q)
	if err != nil {
		return &row{err: err}
	}
	fenced, err := d.fencedWrite(q)
	if err != nil {
		return &row{err: err}
	}
	if fenced {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return &row{err: err}
		}
		return commitRow(queryRow(ctx, tx.Tx, d.postgres, q, a...), tx)
	}
	return queryRow(ctx, d.DB, d.postgres, q, a...)
}
func (d *database) Prepare(q string) (*statement, error) {
	return d.PrepareContext(context.Background(), q)
}
func (d *database) PrepareContext(ctx context.Context, q string) (*statement, error) {
	if fenced, err := d.fencedWrite(q); err != nil || fenced {
		if err == nil {
			err = fmt.Errorf("a leased prepared write requires an explicit transaction")
		}
		return nil, err
	}
	return prepareSQLContext(ctx, d.DB, d.postgres, q)
}
func (d *database) Begin() (*transaction, error) {
	return d.BeginTx(context.Background(), nil)
}
func (d *database) BeginTx(ctx context.Context, opts *sql.TxOptions) (*transaction, error) {
	if d.readOnly {
		return nil, fmt.Errorf("write transactions are unavailable on a read-only backend")
	}
	t, err := d.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return d.guarded(t)
}
func (d *database) Conn(ctx context.Context) (*connection, error) {
	c, err := d.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &connection{Conn: c, postgres: d.postgres, database: d}, nil
}
func (t *transaction) Exec(q string, a ...any) (sql.Result, error) {
	return t.ExecContext(context.Background(), q, a...)
}
func (t *transaction) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	return execSQL(ctx, t.Tx, t.postgres, q, a...)
}
func (t *transaction) Query(q string, a ...any) (*sql.Rows, error) {
	return t.QueryContext(context.Background(), q, a...)
}
func (t *transaction) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	return queryRows(ctx, t.Tx, t.postgres, q, a...)
}
func (t *transaction) QueryRow(q string, a ...any) *row {
	return t.QueryRowContext(context.Background(), q, a...)
}
func (t *transaction) QueryRowContext(ctx context.Context, q string, a ...any) *row {
	return queryRow(ctx, t.Tx, t.postgres, q, a...)
}
func (t *transaction) Prepare(q string) (*statement, error) {
	return t.PrepareContext(context.Background(), q)
}
func (t *transaction) PrepareContext(ctx context.Context, q string) (*statement, error) {
	return prepareSQLContext(ctx, t.Tx, t.postgres, q)
}
func (c *connection) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	fenced, err := c.database.fencedWrite(q)
	if err != nil {
		return nil, err
	}
	if fenced {
		tx, err := c.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		result, err := execSQL(ctx, tx.Tx, c.postgres, q, a...)
		if err != nil {
			return nil, err
		}
		return result, tx.Commit()
	}
	return execSQL(ctx, c.Conn, c.postgres, q, a...)
}
func (c *connection) QueryRowContext(ctx context.Context, q string, a ...any) *row {
	fenced, err := c.database.fencedWrite(q)
	if err != nil {
		return &row{err: err}
	}
	if fenced {
		tx, err := c.BeginTx(ctx, nil)
		if err != nil {
			return &row{err: err}
		}
		return commitRow(queryRow(ctx, tx.Tx, c.postgres, q, a...), tx)
	}
	return queryRow(ctx, c.Conn, c.postgres, q, a...)
}
func (c *connection) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	if fenced, err := c.database.fencedWrite(q); err != nil || fenced {
		if err == nil {
			err = fmt.Errorf("a leased multi-row write requires an explicit transaction")
		}
		return nil, err
	}
	return queryRows(ctx, c.Conn, c.postgres, q, a...)
}
func (c *connection) PrepareContext(ctx context.Context, q string) (*statement, error) {
	if fenced, err := c.database.fencedWrite(q); err != nil || fenced {
		if err == nil {
			err = fmt.Errorf("a leased prepared write requires an explicit transaction")
		}
		return nil, err
	}
	return prepareSQLContext(ctx, c.Conn, c.postgres, q)
}
func (c *connection) BeginTx(ctx context.Context, opts *sql.TxOptions) (*transaction, error) {
	t, err := c.Conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return c.database.guarded(t)
}
