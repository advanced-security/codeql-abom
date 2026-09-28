package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// testCLI is the CodeQL a test in this file says ran its query tests.
const testCLI = "2.27.1"

// shippedQueryTestDDL is the table as it shipped, before a result kept where it stopped or which
// tests it ran.
const shippedQueryTestDDL = `
CREATE TABLE abom_query_test (
  query_id      TEXT NOT NULL,
  query_version TEXT NOT NULL,
  passed        INTEGER NOT NULL,
  total         INTEGER NOT NULL,
  failed        INTEGER NOT NULL,
  detail        TEXT NOT NULL DEFAULT '',
  ran_at        TEXT NOT NULL,
  PRIMARY KEY (query_id, query_version)
);`

// Where a test stopped and which tests it ran are what the authoring steps are read from, so both
// survive being stored, including on a database that recorded tests before either was kept.
func TestARecordedTestKeepsWhereItStoppedAndWhatItRan(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.db.Exec(shippedQueryTestDDL); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO abom_query_test VALUES
		('acme/old', 'v1', 1, 1, 0, '1 of 1 passed', '2026-09-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if err := s.RecordQueryTest(QueryTest{
		QueryID: "acme/rule", QueryVersion: "v1", CLIVersion: testCLI, Total: 1, Failed: 1,
		Detail:       "0 of 1 passed; it does not compile",
		FailureStage: "COMPILATION", TestDigest: "d1",
	}); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.QueryTestFor("acme/rule", "v1", testCLI)
	if err != nil {
		t.Fatal(err)
	}
	if got.FailureStage != "COMPILATION" || got.TestDigest != "d1" {
		t.Errorf("read back stage %q and digest %q", got.FailureStage, got.TestDigest)
	}
	all, err := s.queryTests()
	if err != nil {
		t.Fatal(err)
	}
	if all["acme/rule\x00v1"] != got {
		t.Errorf("the catalog reads %+v where the lookup reads %+v", all["acme/rule\x00v1"], got)
	}

	// Not recorded is not "stopped nowhere": the row reads, with neither filled in, and names no
	// CodeQL because none was kept.
	old, known, err := s.QueryTestFor("acme/old", "v1", "")
	if err != nil || !known {
		t.Fatalf("a result recorded before the columns existed: known=%v err=%v", known, err)
	}
	if old.FailureStage != "" || old.TestDigest != "" || old.CLIVersion != "" {
		t.Errorf("an old result claims stage %q, digest %q and CodeQL %q", old.FailureStage,
			old.TestDigest, old.CLIVersion)
	}
	if _, known, _ := s.QueryTestFor("acme/old", "v1", testCLI); known {
		t.Error("a result from before the CodeQL was kept vouches for the CodeQL running now")
	}
}

// The same query over the same fixtures can answer differently under another CodeQL, so a result
// under one release never stands in for the next, and neither replaces the other.
func TestATestResultBelongsToTheCodeQLThatRanIt(t *testing.T) {
	s := newTestStore(t)
	for _, r := range []QueryTest{
		{QueryID: "acme/rule", QueryVersion: "v1", CLIVersion: "2.27.0", Passed: true, Total: 1},
		{QueryID: "acme/rule", QueryVersion: "v1", CLIVersion: "2.27.1", Total: 1, Failed: 1},
	} {
		if err := s.RecordQueryTest(r); err != nil {
			t.Fatal(err)
		}
	}

	older, _, err := s.QueryTestFor("acme/rule", "v1", "2.27.0")
	if err != nil {
		t.Fatal(err)
	}
	newer, _, err := s.QueryTestFor("acme/rule", "v1", "2.27.1")
	if err != nil {
		t.Fatal(err)
	}
	if !older.Passed || newer.Passed {
		t.Errorf("under 2.27.0 passed=%v, under 2.27.1 passed=%v; each CodeQL keeps its own answer",
			older.Passed, newer.Passed)
	}
	if _, known, _ := s.QueryTestFor("acme/rule", "v1", "2.28.0"); known {
		t.Error("a CodeQL that never ran the tests has a result")
	}
	if err := s.AdvanceQueryStage("acme/rule", "v1", StageTest, "operator", "2.27.1"); err == nil {
		t.Error("a pass under 2.27.0 advanced the version while 2.27.1 is what runs")
	}
	if err := s.RecordQueryTest(QueryTest{QueryID: "acme/rule", QueryVersion: "v2", Passed: true,
		Total: 1}); err == nil {
		t.Error("a result naming no CodeQL was stored, and would vouch for whichever runs next")
	}
}

// A store that keyed a result by version alone is rebuilt, so a second CodeQL's answer adds a row
// instead of overwriting the first one's.
func TestAQueryTestTableKeyedByVersionAloneIsRebuiltWithTheCodeQLInTheKey(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.db.Exec(shippedQueryTestDDL); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO abom_query_test VALUES
		('acme/rule', 'v1', 1, 1, 0, '1 of 1 passed', '2026-09-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.Migrate(); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
	}

	if err := s.RecordQueryTest(QueryTest{QueryID: "acme/rule", QueryVersion: "v1",
		CLIVersion: testCLI, Total: 1, Failed: 1}); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_query_test`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("the table holds %d rows, want the old answer and the new CodeQL's", rows)
	}
	old, known, err := s.QueryTestFor("acme/rule", "v1", "")
	if err != nil || !known || !old.Passed || old.Detail != "1 of 1 passed" {
		t.Errorf("the rebuild lost the old answer: %+v known=%v err=%v", old, known, err)
	}
}

// What the output said about each expectation is the evidence that a test failed for the reason
// expected. Uncounted is not zero: a run that never compared results has nothing to say either way.
func TestATestKeepsItsFixturesAndWhatItsExpectationsSaid(t *testing.T) {
	s := newTestStore(t)
	one, none := int64(1), int64(0)
	for _, r := range []QueryTest{
		{QueryID: "acme/rule", QueryVersion: "stub", CLIVersion: testCLI, Total: 1, Failed: 1,
			FailureStage: "RESULT", FixturesDigest: "f1", Missing: &one, Unexpected: &none},
		{QueryID: "acme/rule", QueryVersion: "broken", CLIVersion: testCLI, Total: 1, Failed: 1,
			FailureStage: "COMPILATION", FixturesDigest: "f1"},
	} {
		if err := s.RecordQueryTest(r); err != nil {
			t.Fatal(err)
		}
	}

	stub, _, err := s.QueryTestFor("acme/rule", "stub", testCLI)
	if err != nil {
		t.Fatal(err)
	}
	if stub.FixturesDigest != "f1" || stub.Missing == nil || *stub.Missing != 1 ||
		stub.Unexpected == nil || *stub.Unexpected != 0 {
		t.Errorf("read back %+v, want fixtures f1, one missing and none unexpected", stub)
	}
	broken, _, err := s.QueryTestFor("acme/rule", "broken", testCLI)
	if err != nil {
		t.Fatal(err)
	}
	if broken.Missing != nil || broken.Unexpected != nil {
		t.Errorf("a run that never compared results reads missing=%v unexpected=%v, want neither",
			broken.Missing, broken.Unexpected)
	}
}

// A query's steps read every version's tests: whether another version passed is part of what
// this one's marks say. Another query's results are not.
func TestTheTestsOfAQueryAreReadAcrossItsVersionsAndNoOtherQuery(t *testing.T) {
	s := newTestStore(t)
	for _, r := range []QueryTest{
		{QueryID: "acme/a", QueryVersion: "v1", CLIVersion: testCLI, Passed: true, Total: 1},
		{QueryID: "acme/a", QueryVersion: "v2", CLIVersion: testCLI, Total: 1, Failed: 1},
		{QueryID: "acme/b", QueryVersion: "v1", CLIVersion: testCLI, Passed: true, Total: 1},
	} {
		if err := s.RecordQueryTest(r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.QueryTestsOf("acme/a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].QueryID != "acme/a" || got[1].QueryID != "acme/a" ||
		got[0].QueryVersion == got[1].QueryVersion {
		t.Errorf("read %+v, want acme/a's two versions", got)
	}
	none, err := s.QueryTestsOf("acme/untested")
	if err != nil || none == nil || len(none) != 0 {
		t.Errorf("a query with no tests read %#v (err %v), want an empty list", none, err)
	}
}

// The one rung that is earned. A ladder every step of which can be set by hand is an audit trail
// of intentions, and the first reader to treat one as a guarantee is misled by something this
// project built.
func TestAVersionNobodyHasTestedCannotBeAdvancedToTest(t *testing.T) {
	s := newTestStore(t)

	err := s.AdvanceQueryStage("acme/rule", "v1", StageTest, "operator", testCLI)
	if err == nil {
		t.Fatal("a version with no tests on record was advanced to test")
	}
	if _, known, _ := s.QueryTestFor("acme/rule", "v1", testCLI); known {
		t.Error("refusing the advance recorded a test result")
	}
}

func TestAVersionWhoseTestsFailCannotBeAdvancedToTest(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordQueryTest(QueryTest{
		QueryID: "acme/rule", QueryVersion: "v1", CLIVersion: testCLI, Passed: false,
		Total: 2, Failed: 1, Detail: "1 of 2 passed",
	}); err != nil {
		t.Fatal(err)
	}

	err := s.AdvanceQueryStage("acme/rule", "v1", StageTest, "operator", testCLI)
	if err == nil {
		t.Fatal("a version whose tests fail was advanced to test")
	}
}

func TestAVersionWhoseTestsPassMayBeAdvanced(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordQueryTest(QueryTest{
		QueryID: "acme/rule", QueryVersion: "v1", CLIVersion: testCLI, Passed: true,
		Total: 2, Failed: 0, Detail: "2 of 2 passed",
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.AdvanceQueryStage("acme/rule", "v1", StageTest, "operator", testCLI); err != nil {
		t.Fatalf("a version whose tests pass was refused: %v", err)
	}
}

// A test result belongs to a version, not to a query. A query that passed and was then edited has
// not passed anything, and a result keyed by id alone would vouch for the edit.
func TestAPassingVersionDoesNotVouchForTheNextOne(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordQueryTest(QueryTest{
		QueryID: "acme/rule", QueryVersion: "v1", CLIVersion: testCLI, Passed: true, Total: 1,
		Detail: "1 of 1 passed",
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.AdvanceQueryStage("acme/rule", "v2", StageTest, "operator", testCLI); err == nil {
		t.Error("an edited query inherited the previous version's green tests")
	}
}

// Only the first rung is gated here. The stages above it are earned by evidence the run and the
// simulation produce, and refusing them on a test result would gate them on the wrong thing.
// Review is earned by a comparison, which reviewgate_test.go holds; here it is enough that what
// refuses it is not a missing test.
func TestTheOtherStagesAreNotGatedByTests(t *testing.T) {
	s := newTestStore(t)

	for _, stage := range []string{StageDev, StageExtendedTest, StageDeploy} {
		if err := s.AdvanceQueryStage("acme/rule", "v1", stage, "operator", testCLI); err != nil {
			t.Errorf("advancing to %s was refused for want of a test: %v", stage, err)
		}
	}
	err := s.AdvanceQueryStage("acme/rule", "v1", StageReview, "operator", testCLI)
	if err == nil || strings.Contains(err.Error(), "test") || !strings.Contains(err.Error(), "compared") {
		t.Errorf("advancing to review returned %v, want a refusal about a comparison, not a test", err)
	}
}

// Never-tested and failing are different answers, and only the second is a defect to go and look
// at. A reader shown one as the other goes looking for a failure that did not happen.
func TestANeverTestedVersionIsNotAFailingOne(t *testing.T) {
	s := newTestStore(t)

	if _, known, err := s.QueryTestFor("acme/rule", "v1", testCLI); err != nil || known {
		t.Errorf("an untested version reports a result: known=%v err=%v", known, err)
	}

	if err := s.RecordQueryTest(QueryTest{
		QueryID: "acme/rule", QueryVersion: "v1", CLIVersion: testCLI, Passed: false, Total: 1,
		Failed: 1,
	}); err != nil {
		t.Fatal(err)
	}
	got, known, err := s.QueryTestFor("acme/rule", "v1", testCLI)
	if err != nil || !known {
		t.Fatalf("a recorded result could not be read back: known=%v err=%v", known, err)
	}
	if got.Passed {
		t.Error("a failing result reads as passing")
	}
}

// A directory holding no tests is not a pass: nothing was shown to work, and reporting it as
// green is the same failure as an unmeasured value printed as zero.
func TestARunThatFoundNoTestsIsNotAPass(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordQueryTest(QueryTest{
		QueryID: "acme/rule", QueryVersion: "v1", CLIVersion: testCLI, Passed: false, Total: 0,
		Detail: "no tests found, so nothing was shown to work",
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.AdvanceQueryStage("acme/rule", "v1", StageTest, "operator", testCLI); err == nil {
		t.Error("a query with no tests at all was advanced to test")
	}
}

// The current state of one version under one CodeQL, not a log of it: a version's tests are
// passing or failing now, and what changed over time is in the ledger of runs.
func TestRecordingTestsAgainReplacesTheAnswer(t *testing.T) {
	s := newTestStore(t)
	for _, passed := range []bool{false, true} {
		if err := s.RecordQueryTest(QueryTest{
			QueryID: "acme/rule", QueryVersion: "v1", CLIVersion: testCLI, Passed: passed, Total: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}

	var rows int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_query_test`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("a version holds %d test rows, want one", rows)
	}
	got, _, err := s.QueryTestFor("acme/rule", "v1", testCLI)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Passed {
		t.Error("the latest answer did not replace the earlier one")
	}
}

// A result with no version belongs to nothing, and storing it would let a later lookup find it
// for a version it never described.
func TestARecordedTestNamesAVersion(t *testing.T) {
	s := newTestStore(t)

	if err := s.RecordQueryTest(QueryTest{QueryID: "acme/rule", CLIVersion: testCLI,
		Passed: true}); err == nil {
		t.Error("a test result with no version was stored")
	}
}
