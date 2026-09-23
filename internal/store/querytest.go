package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// queryTestDDL records what `codeql test run` made of one query version.
//
// Keyed by version rather than by query id: a query that passed its tests and was then edited has
// not passed anything, and a row keyed by id alone would vouch for the edit.
const queryTestDDL = `
CREATE TABLE IF NOT EXISTS abom_query_test (
  query_id      TEXT NOT NULL,
  query_version TEXT NOT NULL,
  passed        INTEGER NOT NULL,
  total         INTEGER NOT NULL,
  failed        INTEGER NOT NULL,
  detail        TEXT NOT NULL DEFAULT '',
  ran_at        TEXT NOT NULL,
  failure_stage TEXT,
  test_digest   TEXT,
  PRIMARY KEY (query_id, query_version)
);`

// QueryTest is the recorded outcome of a version's tests.
type QueryTest struct {
	QueryID      string `json:"queryId"`
	QueryVersion string `json:"queryVersion"`
	Passed       bool   `json:"passed"`
	Total        int    `json:"total"`
	Failed       int    `json:"failed"`
	Detail       string `json:"detail"`
	RanAt        string `json:"ranAt"`
	// FailureStage is where a failing test stopped, in the CLI's words. Empty for a pass, and for
	// a result recorded before the stage was kept.
	FailureStage string `json:"failureStage"`
	// TestDigest identifies the tests that were run, so a result for tests that have since
	// changed can be told from a current one. Empty when nothing identified them.
	TestDigest string `json:"testDigest"`
}

// RecordQueryTest stores what the CLI said about a version, replacing any earlier answer.
//
// Replacing rather than appending: this is the current state of one version, and a version's
// tests can only be passing or failing now. What changed over time is in the ledger of runs.
func (s *Store) RecordQueryTest(t QueryTest) error {
	if t.QueryID == "" || t.QueryVersion == "" {
		return fmt.Errorf("recording a test: a result belongs to a query version, not a query")
	}
	_, err := s.db.Exec(`
INSERT INTO abom_query_test (query_id, query_version, passed, total, failed, detail, ran_at,
                             failure_stage, test_digest)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(query_id, query_version) DO UPDATE SET
  passed = excluded.passed, total = excluded.total, failed = excluded.failed,
  detail = excluded.detail, ran_at = excluded.ran_at,
  failure_stage = excluded.failure_stage, test_digest = excluded.test_digest`,
		t.QueryID, t.QueryVersion, t.Passed, t.Total, t.Failed, t.Detail,
		time.Now().UTC().Format(time.RFC3339), nullable(t.FailureStage), nullable(t.TestDigest))
	if err != nil {
		return fmt.Errorf("recording the tests of %s: %w", t.QueryID, err)
	}
	return nil
}

// QueryTestFor reports what is on record for one version, and whether anything is.
//
// Never-tested is its own answer. A version nobody has tested is not one whose tests failed, and
// only the second is a defect to go and look at.
func (s *Store) QueryTestFor(queryID, version string) (QueryTest, bool, error) {
	var t QueryTest
	var passed int
	err := s.db.QueryRow(`
		SELECT query_id, query_version, passed, total, failed, detail, ran_at,
		       COALESCE(failure_stage, ''), COALESCE(test_digest, '')
		FROM abom_query_test WHERE query_id = ? AND query_version = ?`, queryID, version).
		Scan(&t.QueryID, &t.QueryVersion, &passed, &t.Total, &t.Failed, &t.Detail, &t.RanAt,
			&t.FailureStage, &t.TestDigest)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return QueryTest{}, false, nil
	case err != nil:
		return QueryTest{}, false, fmt.Errorf("reading the tests of %s: %w", queryID, err)
	}
	t.Passed = passed != 0
	return t, true, nil
}

// QueryTestsOf reads what is on record for every version of one query.
func (s *Store) QueryTestsOf(queryID string) ([]QueryTest, error) {
	rows, err := s.db.Query(`
		SELECT query_id, query_version, passed, total, failed, detail, ran_at,
		       COALESCE(failure_stage, ''), COALESCE(test_digest, '')
		FROM abom_query_test WHERE query_id = ? ORDER BY ran_at, query_version`, queryID)
	if err != nil {
		return nil, fmt.Errorf("reading the tests of %s: %w", queryID, err)
	}
	defer rows.Close()

	out := []QueryTest{}
	for rows.Next() {
		var t QueryTest
		var passed int
		if err := rows.Scan(&t.QueryID, &t.QueryVersion, &passed,
			&t.Total, &t.Failed, &t.Detail, &t.RanAt, &t.FailureStage, &t.TestDigest); err != nil {
			return nil, fmt.Errorf("scanning a test of %s: %w", queryID, err)
		}
		t.Passed = passed != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// queryTests reads every recorded outcome, keyed the way the catalog joins.
func (s *Store) queryTests() (map[string]QueryTest, error) {
	out := map[string]QueryTest{}
	rows, err := s.db.Query(`
		SELECT query_id, query_version, passed, total, failed, detail, ran_at,
		       COALESCE(failure_stage, ''), COALESCE(test_digest, '')
		FROM abom_query_test`)
	if err != nil {
		return nil, fmt.Errorf("reading recorded tests: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var t QueryTest
		var passed int
		if err := rows.Scan(&t.QueryID, &t.QueryVersion, &passed,
			&t.Total, &t.Failed, &t.Detail, &t.RanAt, &t.FailureStage, &t.TestDigest); err != nil {
			return nil, fmt.Errorf("scanning a recorded test: %w", err)
		}
		t.Passed = passed != 0
		out[t.QueryID+"\x00"+t.QueryVersion] = t
	}
	return out, rows.Err()
}
