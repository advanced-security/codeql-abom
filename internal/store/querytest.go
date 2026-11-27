package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// queryTestTable is the column body of abom_query_test, which the migration rebuilds an older
// table with.
//
// Keyed by version rather than by query id: a query that passed its tests and was then edited has
// not passed anything, and a row keyed by id alone would vouch for the edit. And by the CodeQL
// that ran them: the same query over the same fixtures can answer differently under another
// release. Empty names a result recorded before the CodeQL was kept, which vouches for none.
const queryTestTable = `(
  query_id        TEXT NOT NULL,
  query_version   TEXT NOT NULL,
  cli_version     TEXT NOT NULL DEFAULT '',
  passed          INTEGER NOT NULL,
  total           INTEGER NOT NULL,
  failed          INTEGER NOT NULL,
  detail          TEXT NOT NULL DEFAULT '',
  ran_at          TEXT NOT NULL,
  failure_stage   TEXT,
  test_digest     TEXT,
  fixtures_digest TEXT,
  missing         INTEGER,
  unexpected      INTEGER,
  marked_fixtures TEXT,
  PRIMARY KEY (query_id, query_version, cli_version)
)`

// queryTestDDL records what `codeql test run` made of one query version.
const queryTestDDL = `CREATE TABLE IF NOT EXISTS abom_query_test ` + queryTestTable + `;`

// queryTestCopyColumns are what a table keyed by version alone held; cli_version takes its default.
const queryTestCopyColumns = `query_id, query_version, passed, total, failed, detail, ran_at,
  failure_stage, test_digest, fixtures_digest, missing, unexpected`

const queryTestColumns = `query_id, query_version, cli_version, passed, total, failed, detail,
  ran_at, COALESCE(failure_stage, ''), COALESCE(test_digest, ''), COALESCE(fixtures_digest, ''),
  missing, unexpected, marked_fixtures`

// QueryTest is the recorded outcome of a version's tests.
type QueryTest struct {
	QueryID      string `json:"queryId"`
	QueryVersion string `json:"queryVersion"`
	// CLIVersion is the CodeQL that ran the tests.
	CLIVersion string `json:"cliVersion"`
	Passed     bool   `json:"passed"`
	Total      int    `json:"total"`
	Failed     int    `json:"failed"`
	Detail     string `json:"detail"`
	RanAt      string `json:"ranAt"`
	// FailureStage is where a failing test stopped, in the CLI's words. Empty for a pass, and for
	// a result recorded before the stage was kept.
	FailureStage string `json:"failureStage"`
	// TestDigest identifies the tests that were run, so a result for tests that have since
	// changed can be told from a current one. Empty when nothing identified them.
	TestDigest string `json:"testDigest"`
	// FixturesDigest identifies the fixtures alone: accepting a report rewrites the .expected and
	// leaves the fixtures as they were.
	FixturesDigest string `json:"fixturesDigest"`
	// Missing and Unexpected count the inline expectations the output did not meet. Nil when no
	// test got as far as comparing results.
	Missing    *int64 `json:"missing"`
	Unexpected *int64 `json:"unexpected"`
	// MarkedFixtures are the fixtures that carried a mark, by path, with a digest of each: what a
	// failure on missing results was seen failing on. Nil for a run recorded before they were kept.
	MarkedFixtures map[string]string `json:"markedFixtures,omitempty"`
}

func scanQueryTest(row interface{ Scan(...any) error }) (QueryTest, error) {
	var t QueryTest
	var passed int
	var missing, unexpected sql.NullInt64
	var marked sql.NullString
	err := row.Scan(&t.QueryID, &t.QueryVersion, &t.CLIVersion, &passed, &t.Total, &t.Failed,
		&t.Detail, &t.RanAt, &t.FailureStage, &t.TestDigest, &t.FixturesDigest, &missing, &unexpected,
		&marked)
	t.Passed = passed != 0
	t.Missing, t.Unexpected = reported(missing), reported(unexpected)
	if err == nil && marked.Valid {
		if err = json.Unmarshal([]byte(marked.String), &t.MarkedFixtures); err != nil {
			err = fmt.Errorf("the marked fixtures of %s %s under CodeQL %s: %w",
				t.QueryID, t.QueryVersion, t.CLIVersion, err)
		} else if t.MarkedFixtures == nil {
			t.MarkedFixtures = map[string]string{}
		}
	}
	return t, err
}

// RecordQueryTest stores what the CLI said about a version, replacing any earlier answer the same
// CodeQL gave.
//
// Replacing rather than appending: this is the current state of one version, and a version's
// tests can only be passing or failing now. What changed over time is in the ledger of runs.
func (s *Store) RecordQueryTest(t QueryTest) error {
	return recordQueryTest(s.db, t)
}

// CheckQueryTest refuses a test result the store would not keep.
func CheckQueryTest(t QueryTest) error {
	switch {
	case t.QueryID == "" || t.QueryVersion == "":
		return fmt.Errorf("recording a test: a result belongs to a query version, not a query")
	case t.CLIVersion == "":
		return fmt.Errorf("recording the tests of %s: a result names the CodeQL that ran it", t.QueryID)
	}
	return nil
}

func recordQueryTest(w authoringWriter, t QueryTest) error {
	if err := CheckQueryTest(t); err != nil {
		return err
	}
	var marked any
	if t.MarkedFixtures != nil {
		body, err := json.Marshal(t.MarkedFixtures)
		if err != nil {
			return fmt.Errorf("recording the tests of %s: %w", t.QueryID, err)
		}
		marked = string(body)
	}
	ranAt := t.RanAt
	if ranAt == "" {
		ranAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := w.Exec(`
INSERT INTO abom_query_test (query_id, query_version, cli_version, passed, total, failed, detail,
                             ran_at, failure_stage, test_digest, fixtures_digest, missing, unexpected,
                             marked_fixtures)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(query_id, query_version, cli_version) DO UPDATE SET
  passed = excluded.passed, total = excluded.total, failed = excluded.failed,
  detail = excluded.detail, ran_at = excluded.ran_at,
  failure_stage = excluded.failure_stage, test_digest = excluded.test_digest,
  fixtures_digest = excluded.fixtures_digest, missing = excluded.missing,
  unexpected = excluded.unexpected, marked_fixtures = excluded.marked_fixtures`,
		t.QueryID, t.QueryVersion, t.CLIVersion, t.Passed, t.Total, t.Failed, t.Detail,
		ranAt, nullable(t.FailureStage), nullable(t.TestDigest),
		nullable(t.FixturesDigest), t.Missing, t.Unexpected, marked)
	if err != nil {
		return fmt.Errorf("recording the tests of %s: %w", t.QueryID, err)
	}
	return nil
}

// QueryTestFor reports what one CodeQL recorded for one version, and whether anything is.
//
// Never-tested is its own answer. A version nobody has tested is not one whose tests failed, and
// only the second is a defect to go and look at.
func (s *Store) QueryTestFor(queryID, version, cli string) (QueryTest, bool, error) {
	t, err := scanQueryTest(s.db.QueryRow(`SELECT `+queryTestColumns+` FROM abom_query_test
		WHERE query_id = ? AND query_version = ? AND cli_version = ?`, queryID, version, cli))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return QueryTest{}, false, nil
	case err != nil:
		return QueryTest{}, false, fmt.Errorf("reading the tests of %s: %w", queryID, err)
	}
	return t, true, nil
}

// QueryTestsOf reads what is on record for every version of one query, under every CodeQL.
// allQueryTests is every test on record, each query's in the order QueryTestsOf reads them.
func (s *Store) allQueryTests() ([]QueryTest, error) {
	rows, err := s.db.Query(`SELECT ` + queryTestColumns + ` FROM abom_query_test
		ORDER BY query_id, ran_at, query_version, cli_version`)
	if err != nil {
		return nil, fmt.Errorf("reading the tests on record: %w", err)
	}
	return collectRows(rows, func(rows *sql.Rows) (QueryTest, error) {
		t, err := scanQueryTest(rows)
		if err != nil {
			return QueryTest{}, fmt.Errorf("scanning a test: %w", err)
		}
		return t, nil
	})
}

func (s *Store) QueryTestsOf(queryID string) ([]QueryTest, error) {
	rows, err := s.db.Query(`SELECT `+queryTestColumns+` FROM abom_query_test
		WHERE query_id = ? ORDER BY ran_at, query_version, cli_version`, queryID)
	if err != nil {
		return nil, fmt.Errorf("reading the tests of %s: %w", queryID, err)
	}
	return collectRows(rows, func(rows *sql.Rows) (QueryTest, error) {
		t, err := scanQueryTest(rows)
		if err != nil {
			return QueryTest{}, fmt.Errorf("scanning a test of %s: %w", queryID, err)
		}
		return t, nil
	})
}

// queryTests reads the newest outcome of each version, keyed the way the catalog joins.
func (s *Store) queryTests() (map[string]QueryTest, error) {
	out := map[string]QueryTest{}
	rows, err := s.db.Query(`SELECT ` + queryTestColumns + ` FROM abom_query_test
		ORDER BY ran_at, cli_version`)
	if err != nil {
		return nil, fmt.Errorf("reading recorded tests: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		t, err := scanQueryTest(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning a recorded test: %w", err)
		}
		out[t.QueryID+"\x00"+t.QueryVersion] = t
	}
	return out, rows.Err()
}

// migrateQueryTestKey rebuilds a query test table keyed by version alone, from before the CodeQL
// that ran a result was part of what the result is. Nothing references the table, so the swap
// needs no pause of the foreign keys.
func (s *Store) migrateQueryTestKey() error {
	var keyed int
	err := s.db.QueryRow(
		`SELECT pk FROM pragma_table_info('abom_query_test') WHERE name = 'cli_version'`).Scan(&keyed)
	switch {
	case err == nil && keyed > 0:
		return nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("reading the key of abom_query_test: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning the query test rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := rebuildTable(tx, "abom_query_test", queryTestTable, queryTestCopyColumns); err != nil {
		return err
	}
	return tx.Commit()
}
