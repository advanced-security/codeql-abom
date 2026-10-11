package store

import (
	"fmt"
	"strings"
	"testing"
)

// A fact restated by a later run must not become a second copy of itself.
func TestReRunningAQueryDoesNotAccumulateCopiesOfTheSameFact(t *testing.T) {
	s := newTestStore(t)
	obs := Observation{
		SubjectKind: SubjectNode, SubjectKey: "octo/app/.github/workflows/ci.yml:build",
		Name: "privileged", Value: "true",
	}
	for _, commit := range []string{"aaa", "bbb", "ccc"} {
		run := observationRunAt(t, s, "abom/surface", commit)
		if _, err := s.AddObservations(run, []Observation{obs}); err != nil {
			t.Fatalf("AddObservations at %s: %v", commit, err)
		}
	}

	got, err := s.ObservationsOf(SubjectNode, obs.SubjectKey)
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("three runs of one query left %d rows, want the one fact they all state", len(got))
	}
}

// Re-statement is not anonymous. Collapsing copies must keep the row attributed, and to the run
// that said it most recently — the same rule that makes a finding name its rule id.
func TestARestatedFactNamesTheRunThatStatedItLast(t *testing.T) {
	s := newTestStore(t)
	obs := Observation{
		SubjectKind: SubjectNode, SubjectKey: "actions/checkout",
		Name: "runsOn", Value: "ubuntu-latest",
	}
	first := observationRunAt(t, s, "abom/first", "aaa")
	if _, err := s.AddObservations(first, []Observation{obs}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	second := observationRunAt(t, s, "abom/second", "bbb")
	if _, err := s.AddObservations(second, []Observation{obs}); err != nil {
		t.Fatalf("second run: %v", err)
	}

	got, err := s.ObservationsOf(SubjectNode, "actions/checkout")
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read back %d rows, want one", len(got))
	}
	if got[0].QueryID != "abom/second" {
		t.Errorf("QueryID = %q, want the run that stated it last", got[0].QueryID)
	}
}

// Deduplication is keyed on the value; disagreeing values are separate statements.
func TestTwoRunsThatDisagreeAboutAFactAreBothKept(t *testing.T) {
	s := newTestStore(t)
	obs := Observation{
		SubjectKind: SubjectNode, SubjectKey: "actions/checkout", Name: "privileged",
	}
	obs.Value = "true"
	if _, err := s.AddObservations(observationRunAt(t, s, "abom/one", "aaa"),
		[]Observation{obs}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	obs.Value = "false"
	if _, err := s.AddObservations(observationRunAt(t, s, "abom/two", "bbb"),
		[]Observation{obs}); err != nil {
		t.Fatalf("second run: %v", err)
	}

	got, err := s.ObservationsOf(SubjectNode, "actions/checkout")
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("read back %d rows, want both statements", len(got))
	}
}

// Empty and absent values are the same statement and must collapse to one row.
func TestAFactWithNoValueStillDeduplicates(t *testing.T) {
	s := newTestStore(t)
	obs := Observation{
		SubjectKind: SubjectNode, SubjectKey: "actions/checkout", Name: "permissions",
	}
	for _, commit := range []string{"aaa", "bbb"} {
		run := observationRunAt(t, s, "abom/surface", commit)
		if _, err := s.AddObservations(run, []Observation{obs}); err != nil {
			t.Fatalf("AddObservations at %s: %v", commit, err)
		}
	}

	got, err := s.ObservationsOf(SubjectNode, "actions/checkout")
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("an unvalued fact stated twice left %d rows, want 1", len(got))
	}
}

// Observation reads must use indexes. The two reads filter different columns, so one index cannot
// serve both: `ObservationsOf` asks by subject, the traversal asks by attribute name.
func TestSQLiteObservationReadsAreIndexedRatherThanScanned(t *testing.T) {
	s := newTestStore(t)

	for _, read := range []struct {
		what  string
		query string
		args  []any
	}{
		{"reading one subject's observations", observationsOfQuery,
			[]any{SubjectNode, "actions/checkout"}},
		{"loading step conditions for the traversal", ConditionObservationsQuery,
			[]any{SubjectNode, ObservationCondition}},
	} {
		plan := explainPlan(t, s, read.query, read.args...)
		for _, step := range plan {
			if strings.Contains(step, "SCAN") && strings.Contains(step, "abom_observation") {
				t.Errorf("%s scans the observation table:\n\t%s",
					read.what, strings.Join(plan, "\n\t"))
			}
		}
	}
}

// Existing databases must be re-keyed in place, keeping the newest duplicate row.
func TestSQLiteMigratingAnOldObservationTableDeduplicatesItKeepingTheNewest(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.db.Exec(`DROP TABLE abom_observation`); err != nil {
		t.Fatalf("removing the re-keyed table: %v", err)
	}
	if _, err := s.db.Exec(legacyObservationDDL); err != nil {
		t.Fatalf("creating the legacy table: %v", err)
	}
	// The legacy key led with query_run_id, so a second run never collided with the first.
	for i, row := range []struct {
		name, value string
		null        bool
	}{
		{"privileged", "false", false},
		{"privileged", "true", false},
		{"privileged", "true", false},
		{"permissions", "", true},
		{"permissions", "", true},
	} {
		run := observationRunAt(t, s, "abom/surface", fmt.Sprintf("commit%d", i))
		var value any
		if !row.null {
			value = row.value
		}
		if _, err := s.db.Exec(`INSERT INTO abom_observation
			(query_run_id, subject_kind, subject_key, name, value)
			VALUES (?, ?, ?, ?, ?)`, run, SubjectNode, "actions/checkout", row.name,
			value); err != nil {
			t.Fatalf("seeding the legacy table: %v", err)
		}
	}

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	got, err := s.ObservationsOf(SubjectNode, "actions/checkout")
	if err != nil {
		t.Fatalf("ObservationsOf: %v", err)
	}
	// "false" and "true" disagree and both survive; the third row is a copy; the two unvalued
	// rows are one statement.
	if len(got) != 3 {
		t.Fatalf("migration left %d rows, want 3: two disagreeing values and one unvalued fact",
			len(got))
	}

	var privileged []string
	for _, o := range got {
		if o.Name == "privileged" {
			privileged = append(privileged, o.Value)
		}
	}
	if len(privileged) != 2 {
		t.Fatalf("privileged kept %v, want both disagreeing values", privileged)
	}

	// The newest row of the duplicate set is the one that must have survived, because the
	// traversal reads the highest row id.
	var newest string
	if err := s.db.QueryRow(`SELECT COALESCE(value,'') FROM abom_observation
		WHERE name = 'privileged' ORDER BY row_id DESC LIMIT 1`).Scan(&newest); err != nil {
		t.Fatalf("reading the surviving row: %v", err)
	}
	if newest != "true" {
		t.Errorf("the newest surviving statement is %q, want the last one recorded", newest)
	}
}

// The rebuild must return freed pages; SQLite otherwise leaves them on the freelist.
func TestSQLiteObservationRebuildReturnsTheSpaceItFrees(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.db.Exec(`DROP TABLE abom_observation`); err != nil {
		t.Fatalf("removing the re-keyed table: %v", err)
	}
	if _, err := s.db.Exec(legacyObservationDDL); err != nil {
		t.Fatalf("creating the legacy table: %v", err)
	}
	for i := range 200 {
		run := observationRunAt(t, s, "abom/surface", fmt.Sprintf("commit%d", i))
		if _, err := s.db.Exec(`INSERT INTO abom_observation
			(query_run_id, subject_kind, subject_key, name, value)
			VALUES (?, ?, ?, ?, ?)`, run, SubjectNode, "actions/checkout", "runsOn",
			"ubuntu-latest"); err != nil {
			t.Fatalf("seeding the legacy table: %v", err)
		}
	}

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var free int
	if err := s.db.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		t.Fatalf("reading the freelist: %v", err)
	}
	if free != 0 {
		t.Errorf("the rebuild left %d pages on the freelist, want the space returned", free)
	}
}

// The legacy shape: keyed on the run, so every re-run wrote a full second copy.
const legacyObservationDDL = `
CREATE TABLE abom_observation (
  row_id       INTEGER PRIMARY KEY,
  query_run_id INTEGER NOT NULL REFERENCES abom_query_run(row_id),
  subject_kind TEXT NOT NULL,
  subject_key  TEXT NOT NULL,
  name         TEXT NOT NULL,
  value        TEXT,
  value_num    REAL,
  file_path    TEXT,
  start_line   INTEGER,
  UNIQUE(query_run_id, subject_kind, subject_key, name)
)`

func observationRunAt(t *testing.T, s *Store, queryID, commit string) int64 {
	t.Helper()
	id, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: queryID, QueryVersion: "1", PackVersion: "0.1.0",
		Repository: "octo/app", DBCommit: commit,
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	return id
}

func explainPlan(t *testing.T, s *Store, query string, args ...any) []string {
	t.Helper()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explaining %q: %v", query, err)
	}
	defer rows.Close()

	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scanning the query plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the query plan: %v", err)
	}
	return plan
}
