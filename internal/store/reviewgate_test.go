package store

import (
	"strings"
	"testing"
)

// finishedComparison compares the held run's analyzed member and finishes, the way a comparison
// that read GitHub would.
func finishedComparison(t *testing.T, s *Store, status string) (runID, id int64) {
	t.Helper()
	runID = comparedHeldRun(t, s)
	id, err := s.CreateComparison(runID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	record := ComparisonRecord{DatabaseIdentity: heldAt(), Status: status, Staleness: StalenessCurrent}
	if status == ComparisonNotCompared {
		record.Reason, record.Detail = ReasonNoCodeScanning, "no analysis found"
	}
	if err := s.RecordComparisonMember(id, record); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishComparison(id); err != nil {
		t.Fatal(err)
	}
	return runID, id
}

// review is what a comparison earns: somebody has seen what this exact version would add to the
// alerts developers already see, under the CodeQL that would run it, before it bites.
func TestReviewIsRefusedUntilARunOfThatVersionIsCompared(t *testing.T) {
	s := newTestStore(t)
	refusal, err := s.ReviewRefusal("actions/unpinned-tag", "u1", "2.27.1")
	if err != nil {
		t.Fatalf("ReviewRefusal: %v", err)
	}
	if !strings.Contains(refusal, "no run of this version") {
		t.Errorf("with no run the refusal is %q", refusal)
	}
	err = s.AdvanceQueryStage("actions/unpinned-tag", "u1", StageReview, "operator", "2.27.1")
	if err == nil || !strings.Contains(err.Error(), refusal) {
		t.Fatalf("advancing with no comparison returned %v, want the refusal", err)
	}

	finishedComparison(t, s, ComparisonCompared)
	if refusal, err := s.ReviewRefusal("actions/unpinned-tag", "u1", "2.27.1"); err != nil || refusal != "" {
		t.Errorf("with a comparison the refusal is %q (%v), want none", refusal, err)
	}
	if err := s.AdvanceQueryStage("actions/unpinned-tag", "u1", StageReview, "operator", "2.27.1"); err != nil {
		t.Errorf("advancing a compared version was refused: %v", err)
	}
}

// A comparison of a run made under another CodeQL describes results this server would not
// produce, and one of another version describes another query.
func TestAComparisonUnderAnotherCodeQLOrOfAnotherVersionEarnsNothing(t *testing.T) {
	s := newTestStore(t)
	finishedComparison(t, s, ComparisonCompared)

	refusal, err := s.ReviewRefusal("actions/unpinned-tag", "u1", "2.28.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(refusal, "2.27.1") || !strings.Contains(refusal, "2.28.0") {
		t.Errorf("under another CodeQL the refusal is %q, want both named", refusal)
	}
	if refusal, _ := s.ReviewRefusal("actions/unpinned-tag", "u2", "2.27.1"); refusal == "" {
		t.Error("a comparison of u1 earned review for u2")
	}
	if refusal, _ := s.ReviewRefusal("actions/unpinned-tag", "u1", ""); refusal == "" {
		t.Error("a server that cannot name its CodeQL advanced a version to review")
	}
}

// A comparison that compared no repository, or stopped before finishing, has shown nothing.
func TestAComparisonThatComparedNothingOrDidNotFinishEarnsNothing(t *testing.T) {
	s := newTestStore(t)
	finishedComparison(t, s, ComparisonNotCompared)
	refusal, err := s.ReviewRefusal("actions/unpinned-tag", "u1", "2.27.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(refusal, "compared no repository") {
		t.Errorf("with nothing compared the refusal is %q", refusal)
	}

	other := newTestStore(t)
	runID := comparedHeldRun(t, other)
	if _, err := other.CreateComparison(runID, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.InterruptAbandonedComparisons(); err != nil {
		t.Fatal(err)
	}
	refusal, err = other.ReviewRefusal("actions/unpinned-tag", "u1", "2.27.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(refusal, ComparisonInterrupted) {
		t.Errorf("with an interrupted comparison the refusal is %q", refusal)
	}
}

// The catalog states the refusal on each row before anybody presses, and it is the same sentence
// the advance refuses with.
func TestTheReviewRefusalsForTheCatalogAreTheAdvancesOwn(t *testing.T) {
	s := newTestStore(t)
	finishedComparison(t, s, ComparisonCompared)
	refusals, err := s.ReviewRefusals("2.28.0")
	if err != nil {
		t.Fatalf("ReviewRefusals: %v", err)
	}
	single, err := s.ReviewRefusal("actions/unpinned-tag", "u1", "2.28.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := refusals.For("actions/unpinned-tag", "u1"); got != single || got == "" {
		t.Errorf("the catalog says %q and the advance %q", got, single)
	}
	if got := refusals.For("acme/never-run", "v1"); !strings.Contains(got, "no run of this version") {
		t.Errorf("a version never run reads %q", got)
	}
}
