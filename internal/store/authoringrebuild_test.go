package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// astTableBeforePrintedBy is the tree table as a store first kept it, when only a session printed.
const astTableBeforePrintedBy = `
CREATE TABLE abom_authoring_ast (
  row_id       INTEGER PRIMARY KEY,
  session_id   INTEGER NOT NULL REFERENCES abom_authoring_session(row_id),
  query_id     TEXT NOT NULL DEFAULT '',
  printed_at   TEXT NOT NULL,
  query        TEXT NOT NULL,
  database     TEXT NOT NULL,
  source_files TEXT NOT NULL DEFAULT '',
  test_digest  TEXT NOT NULL DEFAULT '',
  body         TEXT NOT NULL,
  truncated    INTEGER NOT NULL DEFAULT 0
);`

// A store that kept sessions and trees before drafts and ABOM's own trees existed is rebuilt to hold
// them, and keeps every session, event and tree it had, each still pointing at its own.
func TestSQLiteTablesFromBeforeDraftsAndABOMsTreesAreRebuiltKeepingWhatTheyHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	request, err := s.SaveQueryRequest(aRequest())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DROP TABLE abom_request_draft`, `DROP TABLE abom_authoring_ast`, `DROP TABLE abom_authoring_event`,
		`DROP TABLE abom_authoring_session`, sessionTableBeforeDrafting, astTableBeforePrintedBy,
		`CREATE TABLE abom_authoring_event (
		   row_id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL REFERENCES abom_authoring_session(row_id),
		   at TEXT NOT NULL, hook TEXT NOT NULL, tool TEXT NOT NULL, subject TEXT NOT NULL DEFAULT '',
		   decision TEXT NOT NULL, step TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '',
		   detail TEXT NOT NULL DEFAULT '', took_ms INTEGER)`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO abom_authoring_session (row_id, request_id, state, started_by, model, started_at)
		VALUES (7, ?, 'finished', 'operator', 'claude-opus-5', '2026-09-23T12:00:00Z')`, request); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO abom_authoring_event (session_id, at, hook, tool, decision)
		 VALUES (7, '2026-09-23T12:00:01Z', 'pre-tool-use', 'view', 'allowed')`,
		`INSERT INTO abom_authoring_ast (session_id, query_id, printed_at, query, database, body)
		 VALUES (7, 'acme/a', '2026-09-23T12:00:02Z', 'PrintAST', 'test/A/A.testproj', 'the tree')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrating a store whose sessions and trees predate drafts: %v", err)
	}
	sessions, err := s.AuthoringSessions(request)
	if err != nil || len(sessions) != 1 || sessions[0].RowID != 7 || sessions[0].Events != 1 {
		t.Errorf("sessions after the rebuild %+v %v", sessions, err)
	}
	if trees, err := s.AuthoringASTs(request); err != nil || len(trees) != 1 || trees[0].SessionID != 7 {
		t.Errorf("trees after the rebuild %+v %v", trees, err)
	}
	if _, err := s.StartDraftSession(startedRun(t, s), "acme/a", "operator", "claude-opus-5"); err != nil {
		t.Errorf("a draft after the rebuild: %v", err)
	}
	if _, err := s.RecordAuthoringAST(AuthoringAST{PrintedBy: "operator", QueryID: "acme/a",
		PrintedAt: "2026-09-24T12:00:00Z", Query: "PrintAST", Database: "test/A/A.testproj", Body: "b",
		CLIVersion: testCLI}); err != nil {
		t.Errorf("a tree ABOM printed after the rebuild: %v", err)
	}
	var broken int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&broken); err != nil || broken != 0 {
		t.Errorf("%d rows point at nothing after the rebuild (%v)", broken, err)
	}
	if err := s.Migrate(); err != nil {
		t.Errorf("migrating the rebuilt store again: %v", err)
	}
}
