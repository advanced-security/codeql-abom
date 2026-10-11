package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrateAuthoringOwners rebuilds the session and tree tables of a store that kept them before a
// session could belong to a run and a tree could be ABOM's own. SQLite cannot drop a NOT NULL, and
// neither table has been released, so each is rebuilt rather than worked around.
func (s *Store) migrateAuthoringOwners() error {
	sessions, err := s.notNull("abom_authoring_session", "request_id")
	if err != nil {
		return err
	}
	trees, err := s.notNull("abom_authoring_ast", "session_id")
	if err != nil || (!sessions && !trees) {
		return err
	}

	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserving a connection for the authoring rebuild: %w", err)
	}
	defer func() { _ = conn.Close() }()
	// Off for the swap, on the one connection doing it: dropping a table the timeline references
	// would otherwise delete through it. A PRAGMA inside a transaction is ignored.
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("pausing foreign keys for the authoring rebuild: %w", err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`) }()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning the authoring rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if sessions {
		if err := rebuildTable(tx, "abom_authoring_session", authoringSessionTable, sessionCopyColumns); err != nil {
			return err
		}
	}
	if trees {
		if err := rebuildTable(tx, "abom_authoring_ast", authoringASTTable, astCopyColumns); err != nil {
			return err
		}
	}
	// The indexes went with the old tables.
	for _, ddl := range []string{authoringSessionDDL, authoringASTDDL} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("indexing the rebuilt authoring tables: %w", err)
		}
	}
	var broken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&broken); err != nil {
		return fmt.Errorf("checking the rebuilt authoring tables: %w", err)
	}
	if broken > 0 {
		return fmt.Errorf("the authoring rebuild left %d rows pointing at nothing, so it was undone", broken)
	}
	return tx.Commit()
}

// rebuildTable swaps a table for one of the same name with body's columns, copying every row. The
// new table takes the old one's name, so what references it keeps pointing at it.
func rebuildTable(tx *transaction, table, body, columns string) error {
	for _, stmt := range []string{
		`CREATE TABLE ` + table + `_rebuilt ` + body,
		`INSERT INTO ` + table + `_rebuilt (` + columns + `) SELECT ` + columns + ` FROM ` + table,
		`DROP TABLE ` + table,
		`ALTER TABLE ` + table + `_rebuilt RENAME TO ` + table,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("rebuilding %s: %w", table, err)
		}
	}
	return nil
}

// notNull reports whether a column carries NOT NULL, false where the table does not exist yet.
func (s *Store) notNull(table, column string) (bool, error) {
	var notNull int
	err := s.db.QueryRow(`SELECT "notnull" FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&notNull)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("reading %s.%s: %w", table, column, err)
	}
	return notNull == 1, nil
}
