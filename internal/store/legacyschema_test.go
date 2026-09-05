package store

import (
	"path/filepath"
	"testing"
)

// The shape these tables had before the identity chain existed, written out rather than derived
// from the current schema so that the two can actually disagree. A database in the wild took
// hours of crawling to build and is upgraded in place, so every column added to a CREATE TABLE
// has to be added to addedColumns as well. Forgetting that produced "table abom_suite_query has
// no column named pack_digest" against a real corpus, after unit tests on fresh databases passed.
const legacySchema = `
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

	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if _, err := s.db.Exec(legacySchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func TestAnUpgradedDatabaseAcceptsTheFullRunIdentity(t *testing.T) {
	s := migratedLegacyStore(t)

	run := QueryRun{
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

func TestAnUpgradedDatabaseAcceptsResolvedSuiteContents(t *testing.T) {
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

func TestAnUpgradedDatabaseAcceptsAResultHash(t *testing.T) {
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
