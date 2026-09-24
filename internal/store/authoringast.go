package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// authoringASTDDL keeps what a tool query printed over a target's test database: the tree the agent
// read the fixtures as, which the session's scratch does not outlive.
const authoringASTDDL = `
CREATE TABLE IF NOT EXISTS abom_authoring_ast (
  row_id       INTEGER PRIMARY KEY,
  session_id   INTEGER NOT NULL REFERENCES abom_authoring_session(row_id),
  query_id     TEXT NOT NULL DEFAULT '',
  printed_at   TEXT NOT NULL,
  query        TEXT NOT NULL,
  database     TEXT NOT NULL,
  source_files TEXT NOT NULL DEFAULT '',
  -- Empty where no test named the query when it was printed, which is not a digest of nothing.
  test_digest  TEXT NOT NULL DEFAULT '',
  body         TEXT NOT NULL,
  truncated    INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_authoring_ast_session ON abom_authoring_ast(session_id);
CREATE INDEX IF NOT EXISTS idx_authoring_ast_query ON abom_authoring_ast(query_id, row_id);`

// ErrNoAuthoringAST is a printed tree nobody kept.
var ErrNoAuthoringAST = errors.New("there is no such printed tree")

// AuthoringAST is what a tool query such as PrintAST printed over a target's test database.
type AuthoringAST struct {
	RowID     int64  `json:"rowId"`
	SessionID int64  `json:"sessionId"`
	RequestID int64  `json:"requestId"`
	QueryID   string `json:"queryId"`
	PrintedAt string `json:"printedAt"`
	// Query is the tool query that printed it, PrintAST or PrintCFG.
	Query       string `json:"query"`
	Database    string `json:"database"`
	SourceFiles string `json:"sourceFiles"`
	// TestDigest identifies the fixtures it was printed from, so a tree for changed fixtures reads as old.
	TestDigest string `json:"testDigest"`
	Size       int    `json:"size"`
	Truncated  bool   `json:"truncated"`
	// Body is the tree itself, left out of a listing.
	Body string `json:"body,omitempty"`
}

// RecordAuthoringAST keeps a printed tree against the session that printed it.
func (s *Store) RecordAuthoringAST(a AuthoringAST) (int64, error) {
	if a.SessionID == 0 || a.PrintedAt == "" || strings.TrimSpace(a.Query) == "" ||
		strings.TrimSpace(a.Database) == "" {
		return 0, errors.New("a printed tree names its session, when, the query that printed it and the database")
	}
	res, err := s.db.Exec(`
		INSERT INTO abom_authoring_ast
		  (session_id, query_id, printed_at, query, database, source_files, test_digest, body, truncated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.SessionID, a.QueryID, a.PrintedAt, a.Query, a.Database, a.SourceFiles, a.TestDigest, a.Body,
		a.Truncated)
	if err != nil {
		return 0, fmt.Errorf("keeping what %s printed in session %d: %w", a.Query, a.SessionID, err)
	}
	// A plain INSERT, so LastInsertId is the row it just wrote.
	return res.LastInsertId()
}

const astColumns = `a.row_id, a.session_id, s.request_id, a.query_id, a.printed_at, a.query, a.database,
	a.source_files, a.test_digest, LENGTH(CAST(a.body AS BLOB)), a.truncated`

func scanAST(row interface{ Scan(...any) error }, body *string) (AuthoringAST, error) {
	var a AuthoringAST
	dest := []any{&a.RowID, &a.SessionID, &a.RequestID, &a.QueryID, &a.PrintedAt, &a.Query, &a.Database,
		&a.SourceFiles, &a.TestDigest, &a.Size, &a.Truncated}
	if body != nil {
		dest = append(dest, body)
	}
	err := row.Scan(dest...)
	return a, err
}

// AuthoringASTs lists the trees printed in a request's sessions, newest first, without their bodies.
func (s *Store) AuthoringASTs(request int64) ([]AuthoringAST, error) {
	rows, err := s.db.Query(`
		SELECT `+astColumns+`
		FROM abom_authoring_ast a JOIN abom_authoring_session s ON s.row_id = a.session_id
		WHERE s.request_id = ?
		ORDER BY a.row_id DESC`, request)
	if err != nil {
		return nil, fmt.Errorf("reading the trees printed for request %d: %w", request, err)
	}
	defer rows.Close()

	out := []AuthoringAST{}
	for rows.Next() {
		a, err := scanAST(rows, nil)
		if err != nil {
			return nil, fmt.Errorf("scanning a printed tree: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AuthoringAST reads one printed tree with its body.
func (s *Store) AuthoringAST(id int64) (AuthoringAST, error) {
	var body string
	a, err := scanAST(s.db.QueryRow(`
		SELECT `+astColumns+`, a.body
		FROM abom_authoring_ast a JOIN abom_authoring_session s ON s.row_id = a.session_id
		WHERE a.row_id = ?`, id), &body)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return AuthoringAST{}, fmt.Errorf("printed tree %d: %w", id, ErrNoAuthoringAST)
	case err != nil:
		return AuthoringAST{}, fmt.Errorf("reading printed tree %d: %w", id, err)
	}
	a.Body = body
	return a, nil
}

// NewestASTs is the newest PrintAST tree kept for each query, without bodies.
func (s *Store) NewestASTs() (map[string]AuthoringAST, error) {
	rows, err := s.db.Query(`
		SELECT ` + astColumns + `
		FROM abom_authoring_ast a JOIN abom_authoring_session s ON s.row_id = a.session_id
		WHERE a.row_id IN (
		  SELECT MAX(row_id) FROM abom_authoring_ast
		  WHERE query = 'PrintAST' AND query_id <> '' GROUP BY query_id)`)
	if err != nil {
		return nil, fmt.Errorf("reading the newest printed trees: %w", err)
	}
	defer rows.Close()

	out := map[string]AuthoringAST{}
	for rows.Next() {
		a, err := scanAST(rows, nil)
		if err != nil {
			return nil, fmt.Errorf("scanning a printed tree: %w", err)
		}
		out[a.QueryID] = a
	}
	return out, rows.Err()
}
