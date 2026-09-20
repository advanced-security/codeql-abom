package store

import (
	"fmt"
	"strings"
	"testing"
)

// A fact restated by a later run must not become a second copy of itself.
//
// The table's UNIQUE key led with `query_run_id`, which is new for every run, so re-analysis at a
// new commit or after a pack bump wrote a complete second copy of everything the query said. On
// the reference corpus that was **140,421 rows carrying 41,565 distinct facts** — a mean of 3.38
// copies and a maximum of 37 — while `AddObservations` documented the opposite guarantee.
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

// Deduplication is keyed on the value, never on the fact. Two runs that genuinely disagree are
// two statements, and picking a winner is a judgement this layer does not make — 1,614 of the
// reference corpus's 41,565 facts disagree across their copies.
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

// An empty value and an absent one are the same statement, and must collapse to one row.
//
// They did not: `value` was nullable and NULL never equals NULL in a UNIQUE constraint, so a fact
// with no value could never deduplicate against itself. **10,755 of the corpus's 140,421 rows
// carry a NULL value**, and both readers already `COALESCE` it to the empty string — so the two
// spellings were indistinguishable on the way out and distinct on the way in.
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

// Neither reader may scan the largest table in the database.
//
// The only index was the autoindex behind the old UNIQUE key, and its leading column was
// `query_run_id` — which **no read filters on**. So 16.5 MiB of index served no query while both
// readers scanned 140,421 rows. The two reads filter different columns, so one index cannot serve
// both: `ObservationsOf` asks by subject, the traversal asks by attribute name.
//
// The plan is asserted rather than the clock, because the cost is structural and a stopwatch on a
// small test corpus would pass either way.
func TestTheObservationReadsAreIndexedRatherThanScanned(t *testing.T) {
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

// An existing database has to be re-keyed in place, and the rows it already holds deduplicated,
// or the fix only helps corpora created after it.
//
// Last-wins is what the traversal already relies on: `loadConditions` orders by `row_id` and lets
// the highest win, so the surviving row of a duplicate set must be the newest one. Keeping the
// earliest would silently rewrite history to the first thing anybody said.
func TestMigratingAnOldObservationTableDeduplicatesItKeepingTheNewest(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.db.Exec(`DROP TABLE abom_observation`); err != nil {
		t.Fatalf("removing the re-keyed table: %v", err)
	}
	if _, err := s.db.Exec(legacyObservationDDL); err != nil {
		t.Fatalf("creating the legacy table: %v", err)
	}
	// Each copy comes from its own run, which is how the corpus accumulated them: the old key led
	// with query_run_id, so a second run never collided with the first.
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

// The rebuild must give the freed space back, or an upgrade makes the file bigger.
//
// Dropping 97,042 rows leaves their pages on the freelist, and SQLite does not return those to
// the filesystem on its own. Measured on the reference corpus: the migration alone took it from
// 80.82 MiB to **92.96 MiB** — a headline of "33.8% smaller" delivered as 15% larger — and
// reclaiming 8,470 free pages costs 0.38 s once.
func TestTheObservationRebuildReturnsTheSpaceItFrees(t *testing.T) {
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
