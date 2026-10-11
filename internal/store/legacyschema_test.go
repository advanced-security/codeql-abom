package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// The legacy shape is written out rather than derived so it can disagree with the current schema.
// Any column added to CREATE TABLE must also be added to addedColumns for in-place upgrades.
const legacySchema = `
CREATE TABLE abom_owned_scope (
  owner         TEXT PRIMARY KEY,
  kind          TEXT NOT NULL,
  enabled       INTEGER NOT NULL DEFAULT 1,
  includes_only INTEGER NOT NULL DEFAULT 0,
  declared_at   TEXT NOT NULL
);
CREATE TABLE abom_query_run (
  row_id       INTEGER PRIMARY KEY,
  run_key      TEXT NOT NULL UNIQUE,
  query_id     TEXT NOT NULL,
  query_version TEXT,
  pack_version TEXT,
  repository   TEXT NOT NULL,
  db_commit    TEXT,
  ran_at       TEXT NOT NULL
);
CREATE TABLE abom_suite_query (
  suite        TEXT NOT NULL,
  pack         TEXT NOT NULL,
  pack_version TEXT,
  query_id     TEXT NOT NULL,
  kind         TEXT,
  resolved_at  TEXT NOT NULL,
  PRIMARY KEY (suite, query_id)
);
CREATE TABLE abom_frontier (
  node_key        TEXT PRIMARY KEY,
  owner           TEXT,
  repo            TEXT,
  subpath         TEXT,
  depth           INTEGER NOT NULL,
  status          TEXT NOT NULL,
  attempts        INTEGER NOT NULL DEFAULT 0,
  score           REAL NOT NULL DEFAULT 0,
  cost_estimate   REAL NOT NULL DEFAULT 1,
  reason          TEXT,
  discovered_from TEXT,
  enqueued_at     TEXT NOT NULL,
  next_attempt_at TEXT,
  completed_at    TEXT,
  claimed_at      TEXT,
  analyzed_ref    TEXT,
  resolved_sha    TEXT,
  analyzed_at     TEXT,
  checked_at      TEXT,
  analysis_count  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE abom_finding (
  row_id            INTEGER PRIMARY KEY,
  query_run_id      INTEGER,
  rule_id           TEXT NOT NULL,
  repository_row_id INTEGER NOT NULL,
  file_path         TEXT,
  start_line        INTEGER,
  start_column      INTEGER,
  end_line          INTEGER,
  end_column        INTEGER,
  message           TEXT,
  severity          TEXT,
  security_severity REAL,
  fingerprint       TEXT NOT NULL DEFAULT '',
  UNIQUE(rule_id, repository_row_id, file_path, start_line, start_column, fingerprint)
);
`

// migratedLegacyStore opens a database already holding the pre-identity tables, so Migrate has to
// widen them rather than create them.
func migratedLegacyStore(t *testing.T) *Store {
	t.Helper()
	return migratedLegacyStoreWith(t, nil)
}

// migratedLegacyStoreWith seeds the legacy tables before migrating. A migration that rewrites a
// table can only be tested against one that already holds rows.
func migratedLegacyStoreWith(t *testing.T, seed func(*Store)) *Store {
	t.Helper()
	if !strings.HasPrefix(t.Name(), "TestSQLite") {
		t.Fatal("a historical SQLite schema fixture requires an explicit TestSQLite test name")
	}

	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if _, err := s.db.Exec(legacySchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if seed != nil {
		seed(s)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func TestSQLiteUpgradedDatabaseAcceptsTheFullRunIdentity(t *testing.T) {
	s := migratedLegacyStore(t)

	run := QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "abom/edges", QueryVersion: "1.0.0",
		Pack: "advanced-security/abom-actions-graph-extraction-src", PackVersion: "2.26.4",
		PackDigest: "ab12cd34", CLIVersion: "2.26.4",
		Repository: "octo/app", DBCommit: "deadbeef",
	}
	if _, err := s.RecordQueryRun(run); err != nil {
		t.Fatalf("RecordQueryRun on an upgraded database: %v", err)
	}

	var pack, digest, cli string
	err := s.db.QueryRow(
		`SELECT pack, pack_digest, cli_version FROM abom_query_run WHERE query_id = ?`,
		run.QueryID,
	).Scan(&pack, &digest, &cli)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if pack != run.Pack || digest != run.PackDigest || cli != run.CLIVersion {
		t.Errorf("stored %s/%s/%s, want %s/%s/%s",
			pack, digest, cli, run.Pack, run.PackDigest, run.CLIVersion)
	}
}

func TestSQLiteUpgradedDatabaseAcceptsResolvedSuiteContents(t *testing.T) {
	s := migratedLegacyStore(t)

	queries := []SuiteQuery{{
		Pack: "codeql/actions-queries", PackVersion: "0.6.34", PackDigest: "ef56ab78",
		QueryID: "actions/unpinned-tag", Kind: "problem",
	}}
	if err := s.RecordSuiteContents(findingsSuite, queries); err != nil {
		t.Fatalf("RecordSuiteContents on an upgraded database: %v", err)
	}

	got, err := s.SuiteContents()
	if err != nil {
		t.Fatalf("SuiteContents: %v", err)
	}
	if len(got[findingsSuite]) != 1 {
		t.Fatalf("read back %d queries, want 1", len(got[findingsSuite]))
	}
	if digest := got[findingsSuite][0].PackDigest; digest != "ef56ab78" {
		t.Errorf("pack digest %q, want ef56ab78", digest)
	}
}

func TestSQLiteUpgradedDatabaseAcceptsAResultHash(t *testing.T) {
	s := migratedLegacyStore(t)

	repoID, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	finding := Finding{
		RuleID: "actions/unpinned-tag", RepositoryID: repoID,
		FilePath: ".github/workflows/ci.yml", StartLine: 12,
		ResultHash: "9f8e7d6c",
	}
	if _, err := s.AddFindings([]Finding{finding}); err != nil {
		t.Fatalf("AddFindings on an upgraded database: %v", err)
	}

	var got string
	if err := s.db.QueryRow(
		`SELECT COALESCE(result_hash,'') FROM abom_finding WHERE rule_id = ?`,
		finding.RuleID).Scan(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != finding.ResultHash {
		t.Errorf("stored result hash %q, want %q", got, finding.ResultHash)
	}
}

func TestSQLiteUpgradedDatabaseCanExpediteAQueuedItem(t *testing.T) {
	s := migratedLegacyStore(t)

	if _, err := s.db.Exec(`
		INSERT INTO abom_frontier (node_key, owner, repo, depth, status, enqueued_at)
		VALUES ('octo/mine', 'octo', 'mine', 1, 'pending', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}

	if _, err := s.db.Exec(
		`UPDATE abom_frontier SET expedited_at = ? WHERE node_key = ?`,
		"2026-01-02T00:00:00Z", "octo/mine"); err != nil {
		t.Fatalf("expediting on an upgraded database: %v", err)
	}
}

// The queue was keyed by dependency alone, which allowed exactly one analyzed tree per
// dependency. Re-keying it needs a table rebuild, and a rebuild is where rows go missing: this
// upgrades a legacy database with work already in it and checks every row and every column
// survived, then that the new key admits a second reference for the same dependency.
func TestSQLiteUpgradingReKeysTheQueueWithoutLosingWork(t *testing.T) {
	s := migratedLegacyStoreWith(t, func(s *Store) {
		if _, err := s.db.Exec(`
			INSERT INTO abom_frontier
				(node_key, owner, repo, depth, status, attempts, score, enqueued_at,
				 resolved_sha, analysis_count)
			VALUES ('actions/checkout', 'actions', 'checkout', 1, 'done', 2, 9.5,
			        '2026-01-01T00:00:00Z', 'abc123', 3)`); err != nil {
			t.Fatalf("seeding legacy work: %v", err)
		}
	})

	var status, sha, ref string
	var attempts, count int
	var score float64
	if err := s.db.QueryRow(`
		SELECT status, COALESCE(resolved_sha,''), ref, attempts, score, analysis_count
		FROM abom_frontier WHERE node_key = 'actions/checkout'`).
		Scan(&status, &sha, &ref, &attempts, &score, &count); err != nil {
		t.Fatalf("the row did not survive the rebuild: %v", err)
	}
	if status != "done" || sha != "abc123" || attempts != 2 || score != 9.5 || count != 3 {
		t.Errorf("row came back as %q/%q/%d/%v/%d, want its recorded values",
			status, sha, attempts, score, count)
	}
	// Empty, not "HEAD": the row was analyzed at whatever the default branch was, and inventing
	// a ref name it never recorded would be a claim rather than a migration.
	if ref != "" {
		t.Errorf("ref = %q, want empty for a row analyzed before refs were tracked", ref)
	}

	if _, err := s.db.Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('actions/checkout', 'v3', 'actions', 'checkout', 1, 'pending',
		        '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("a second reference for one dependency was refused: %v", err)
	}

	var rows int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM abom_frontier WHERE node_key = 'actions/checkout'`).
		Scan(&rows); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if rows != 2 {
		t.Errorf("the dependency has %d rows, want one per reference", rows)
	}
}
