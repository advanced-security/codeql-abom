package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// authoringASTTable is the tree table's columns. A tree names the session that printed it, or who
// asked ABOM to print it, never both.
const authoringASTTable = `(
  row_id       INTEGER PRIMARY KEY,
  session_id   INTEGER REFERENCES abom_authoring_session(row_id),
  printed_by   TEXT NOT NULL DEFAULT '',
  query_id     TEXT NOT NULL DEFAULT '',
  printed_at   TEXT NOT NULL,
  query        TEXT NOT NULL,
  database     TEXT NOT NULL,
  source_files TEXT NOT NULL DEFAULT '',
  -- Empty where no test named the query when it was printed, which is not a digest of nothing.
  test_digest  TEXT NOT NULL DEFAULT '',
  body         TEXT NOT NULL,
  truncated    INTEGER NOT NULL DEFAULT 0,
  -- The CodeQL whose extractor built the database the tree reads; NULL from before it was kept.
  cli_version  TEXT,
  -- The SHA-256 of the body, which events name the tree by; empty for a tree kept before they did.
  digest       TEXT NOT NULL DEFAULT ''
)`

// astCopyColumns are the columns a rebuild carries forward.
const astCopyColumns = `row_id, session_id, printed_by, query_id, printed_at, query, database, source_files,
  test_digest, body, truncated, cli_version, digest`

// authoringASTDDL keeps what a tool query printed over a target's test database: the tree the agent
// read the fixtures as, which the session's scratch does not outlive, or one ABOM printed itself.
const authoringASTDDL = `
CREATE TABLE IF NOT EXISTS abom_authoring_ast ` + authoringASTTable + `;

CREATE INDEX IF NOT EXISTS idx_authoring_ast_session ON abom_authoring_ast(session_id);
CREATE INDEX IF NOT EXISTS idx_authoring_ast_query ON abom_authoring_ast(query_id, row_id);`

// ErrNoAuthoringAST is a printed tree nobody kept.
var ErrNoAuthoringAST = errors.New("there is no such printed tree")

// AuthoringAST is what a tool query such as PrintAST printed over a target's test database.
type AuthoringAST struct {
	RowID int64 `json:"rowId"`
	// SessionID and RequestID are zero for a tree ABOM printed, which names PrintedBy instead.
	SessionID int64  `json:"sessionId"`
	RequestID int64  `json:"requestId"`
	PrintedBy string `json:"printedBy"`
	QueryID   string `json:"queryId"`
	PrintedAt string `json:"printedAt"`
	// Query is the tool query that printed it, PrintAST or PrintCFG.
	Query       string `json:"query"`
	Database    string `json:"database"`
	SourceFiles string `json:"sourceFiles"`
	// TestDigest identifies the fixtures it was printed from, so a tree for changed fixtures reads as old.
	TestDigest string `json:"testDigest"`
	// CLIVersion is the CodeQL that printed it, whose extractor shaped what the tree says.
	CLIVersion string `json:"cliVersion"`
	Size       int    `json:"size"`
	Truncated  bool   `json:"truncated"`
	// Digest names the tree by what it says, as an event that printed it does; empty for a tree kept
	// before events named trees that way.
	Digest string `json:"digest"`
	// Body is the tree itself, left out of a listing.
	Body string `json:"body,omitempty"`
}

// TreeDigest names a printed tree by what it says: the SHA-256 of its body, in hex.
func TreeDigest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// RecordAuthoringAST keeps a printed tree against the session that printed it, or who asked ABOM to.
func (s *Store) RecordAuthoringAST(a AuthoringAST) (int64, error) {
	return recordAuthoringAST(s.db, a)
}

// CheckAuthoringAST refuses a printed tree the store would not keep.
func CheckAuthoringAST(a AuthoringAST) error {
	if (a.SessionID == 0) == (strings.TrimSpace(a.PrintedBy) == "") || a.PrintedAt == "" ||
		strings.TrimSpace(a.Query) == "" || strings.TrimSpace(a.Database) == "" || a.CLIVersion == "" {
		return errors.New("a printed tree names its session or who asked ABOM to print it, when, the " +
			"query that printed it, the database and the CodeQL that printed it")
	}
	if a.Digest != "" && a.Digest != TreeDigest(a.Body) {
		return fmt.Errorf("what %s printed is not the tree named %s", a.Query, a.Digest)
	}
	return nil
}

func recordAuthoringAST(w authoringWriter, a AuthoringAST) (int64, error) {
	if err := CheckAuthoringAST(a); err != nil {
		return 0, err
	}
	digest := TreeDigest(a.Body)
	var session any
	if a.SessionID != 0 {
		session = a.SessionID
	}
	res, err := w.Exec(`
		INSERT INTO abom_authoring_ast
		  (session_id, printed_by, query_id, printed_at, query, database, source_files, test_digest, body,
		   truncated, cli_version, digest)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session, strings.TrimSpace(a.PrintedBy), a.QueryID, a.PrintedAt, a.Query, a.Database, a.SourceFiles,
		a.TestDigest, a.Body, a.Truncated, a.CLIVersion, digest)
	if err != nil {
		return 0, fmt.Errorf("keeping what %s printed: %w", a.Query, err)
	}
	// A plain INSERT, so LastInsertId is the row it just wrote.
	return res.LastInsertId()
}

const astColumns = `a.row_id, COALESCE(a.session_id, 0), COALESCE(s.request_id, 0), a.printed_by, a.query_id,
	a.printed_at, a.query, a.database, a.source_files, a.test_digest, COALESCE(a.cli_version, ''),
	LENGTH(CAST(a.body AS BLOB)), a.truncated, a.digest`

// astFrom reaches a tree's request through its session, which a tree ABOM printed does not have.
const astFrom = `FROM abom_authoring_ast a LEFT JOIN abom_authoring_session s ON s.row_id = a.session_id`

func scanAST(row interface{ Scan(...any) error }, body *string) (AuthoringAST, error) {
	var a AuthoringAST
	dest := []any{&a.RowID, &a.SessionID, &a.RequestID, &a.PrintedBy, &a.QueryID, &a.PrintedAt, &a.Query,
		&a.Database, &a.SourceFiles, &a.TestDigest, &a.CLIVersion, &a.Size, &a.Truncated, &a.Digest}
	if body != nil {
		dest = append(dest, body)
	}
	err := row.Scan(dest...)
	return a, err
}

// AuthoringASTs lists the trees printed in a request's sessions, newest first, without their bodies.
func (s *Store) AuthoringASTs(request int64) ([]AuthoringAST, error) {
	return s.listASTs(`s.request_id = ?`, request)
}

// QueryASTs lists the trees printed for a query, by a session or by ABOM, newest first, without
// their bodies.
func (s *Store) QueryASTs(queryID string) ([]AuthoringAST, error) {
	return s.listASTs(`a.query_id = ?`, queryID)
}

func (s *Store) listASTs(where string, arg any) ([]AuthoringAST, error) {
	rows, err := s.db.Query(`SELECT `+astColumns+` `+astFrom+` WHERE `+where+` ORDER BY a.row_id DESC`, arg)
	if err != nil {
		return nil, fmt.Errorf("reading printed trees: %w", err)
	}
	return collectRows(rows, func(rows *sql.Rows) (AuthoringAST, error) {
		a, err := scanAST(rows, nil)
		if err != nil {
			return AuthoringAST{}, fmt.Errorf("scanning a printed tree: %w", err)
		}
		return a, nil
	})
}

// AuthoringAST reads one printed tree with its body.
func (s *Store) AuthoringAST(id int64) (AuthoringAST, error) {
	var body string
	a, err := scanAST(s.db.QueryRow(`SELECT `+astColumns+`, a.body `+astFrom+` WHERE a.row_id = ?`, id), &body)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return AuthoringAST{}, fmt.Errorf("printed tree %d: %w", id, ErrNoAuthoringAST)
	case err != nil:
		return AuthoringAST{}, fmt.Errorf("reading printed tree %d: %w", id, err)
	}
	a.Body = body
	return a, nil
}

// AuthoringASTByDigest reads, with its body, the tree a session printed that an event names by digest.
func (s *Store) AuthoringASTByDigest(session int64, digest string) (AuthoringAST, error) {
	var body string
	a, err := scanAST(s.db.QueryRow(`SELECT `+astColumns+`, a.body `+astFrom+`
		WHERE a.session_id = ? AND a.digest = ? ORDER BY a.row_id LIMIT 1`, session, digest), &body)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return AuthoringAST{}, fmt.Errorf("session %d printed no tree %s: %w", session, digest, ErrNoAuthoringAST)
	case err != nil:
		return AuthoringAST{}, fmt.Errorf("reading a tree session %d printed: %w", session, err)
	}
	a.Body = body
	return a, nil
}

// NewestASTs is the newest PrintAST tree kept for each query, whoever printed it, without bodies.
func (s *Store) NewestASTs() (map[string]AuthoringAST, error) {
	rows, err := s.db.Query(`
		SELECT ` + astColumns + ` ` + astFrom + `
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
