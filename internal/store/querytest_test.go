package store

import "testing"

// The one rung that is earned. A ladder every step of which can be set by hand is an audit trail
// of intentions, and the first reader to treat one as a guarantee is misled by something this
// project built.
func TestAVersionNobodyHasTestedCannotBeAdvancedToTest(t *testing.T) {
	s := newTestStore(t)

	err := s.AdvanceQueryStage("acme/rule", "v1", StageTest, "operator")
	if err == nil {
		t.Fatal("a version with no tests on record was advanced to test")
	}
	if _, known, _ := s.QueryTestFor("acme/rule", "v1"); known {
		t.Error("refusing the advance recorded a test result")
	}
}

func TestAVersionWhoseTestsFailCannotBeAdvancedToTest(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordQueryTest(QueryTest{
		QueryID: "acme/rule", QueryVersion: "v1", Passed: false,
		Total: 2, Failed: 1, Detail: "1 of 2 passed",
	}); err != nil {
		t.Fatal(err)
	}

	err := s.AdvanceQueryStage("acme/rule", "v1", StageTest, "operator")
	if err == nil {
		t.Fatal("a version whose tests fail was advanced to test")
	}
}

func TestAVersionWhoseTestsPassMayBeAdvanced(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordQueryTest(QueryTest{
		QueryID: "acme/rule", QueryVersion: "v1", Passed: true,
		Total: 2, Failed: 0, Detail: "2 of 2 passed",
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.AdvanceQueryStage("acme/rule", "v1", StageTest, "operator"); err != nil {
		t.Fatalf("a version whose tests pass was refused: %v", err)
	}
}

// A test result belongs to a version, not to a query. A query that passed and was then edited has
// not passed anything, and a result keyed by id alone would vouch for the edit.
func TestAPassingVersionDoesNotVouchForTheNextOne(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordQueryTest(QueryTest{
		QueryID: "acme/rule", QueryVersion: "v1", Passed: true, Total: 1, Detail: "1 of 1 passed",
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.AdvanceQueryStage("acme/rule", "v2", StageTest, "operator"); err == nil {
		t.Error("an edited query inherited the previous version's green tests")
	}
}

// Only the first rung is gated here. The stages above it are earned by evidence the run and the
// simulation produce, and refusing them on a test result would gate them on the wrong thing.
func TestTheOtherStagesAreNotGatedByTests(t *testing.T) {
	s := newTestStore(t)

	for _, stage := range []string{StageDev, StageExtendedTest, StageReview, StageDeploy} {
		if err := s.AdvanceQueryStage("acme/rule", "v1", stage, "operator"); err != nil {
			t.Errorf("advancing to %s was refused for want of a test: %v", stage, err)
		}
	}
}

// Never-tested and failing are different answers, and only the second is a defect to go and look
// at. A reader shown one as the other goes looking for a failure that did not happen.
func TestANeverTestedVersionIsNotAFailingOne(t *testing.T) {
	s := newTestStore(t)

	if _, known, err := s.QueryTestFor("acme/rule", "v1"); err != nil || known {
		t.Errorf("an untested version reports a result: known=%v err=%v", known, err)
	}

	if err := s.RecordQueryTest(QueryTest{
		QueryID: "acme/rule", QueryVersion: "v1", Passed: false, Total: 1, Failed: 1,
	}); err != nil {
		t.Fatal(err)
	}
	got, known, err := s.QueryTestFor("acme/rule", "v1")
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
		QueryID: "acme/rule", QueryVersion: "v1", Passed: false, Total: 0,
		Detail: "no tests found, so nothing was shown to work",
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.AdvanceQueryStage("acme/rule", "v1", StageTest, "operator"); err == nil {
		t.Error("a query with no tests at all was advanced to test")
	}
}

// The current state of one version, not a log of it: a version's tests are passing or failing
// now, and what changed over time is in the ledger of runs.
func TestRecordingTestsAgainReplacesTheAnswer(t *testing.T) {
	s := newTestStore(t)
	for _, passed := range []bool{false, true} {
		if err := s.RecordQueryTest(QueryTest{
			QueryID: "acme/rule", QueryVersion: "v1", Passed: passed, Total: 1,
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
	got, _, err := s.QueryTestFor("acme/rule", "v1")
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

	if err := s.RecordQueryTest(QueryTest{QueryID: "acme/rule", Passed: true}); err == nil {
		t.Error("a test result with no version was stored")
	}
}
