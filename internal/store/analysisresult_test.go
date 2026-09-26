package store

import "testing"

// A member's total is the run's; tuning one of two queries needs that query's own count.
func TestAMemberKeepsWhatEachQueryFoundInIt(t *testing.T) {
	s := newTestStore(t)
	id := startedRun(t, s)
	target := DatabaseIdentity{
		Repository: "octo/app", Commit: "sha-one", SourceFilter: "f0", Mode: AnalysisModeRegular,
	}
	if err := s.RecordAnalysisMemberResult(id, target, MemberResult{
		Status: AnalysisSucceeded, Results: 3, Counted: true,
		ByQuery: map[string]int{"actions/unpinned-tag": 3, "acme/quiet": 0},
	}); err != nil {
		t.Fatal(err)
	}

	by := map[string]AnalysisMember{}
	run, _ := s.AnalysisRun(id)
	for _, m := range run.Members {
		by[m.Repository] = m
	}
	got := by["octo/app"].ByQuery
	if got["actions/unpinned-tag"] != 3 {
		t.Errorf("the member credits unpinned-tag with %d, want 3", got["actions/unpinned-tag"])
	}
	if n, ok := got["acme/quiet"]; !ok || n != 0 {
		t.Errorf("a query that looked and found nothing reads %d (recorded %v), want a zero", n, ok)
	}
	// Nothing ran against this one, so no query has a count there -- not even a zero.
	if unanalyzed := by["octo/unanalyzed"].ByQuery; unanalyzed == nil || len(unanalyzed) != 0 {
		t.Errorf("a member nothing ran against carries %v, want an empty set", unanalyzed)
	}
}

// A re-run replaces each query's count rather than adding to it, or every count doubles.
func TestRecordingAMemberAgainReplacesWhatEachQueryFound(t *testing.T) {
	s := newTestStore(t)
	id := startedRun(t, s)
	target := DatabaseIdentity{
		Repository: "octo/app", Commit: "sha-one", SourceFilter: "f0", Mode: AnalysisModeRegular,
	}
	for _, n := range []int{5, 2} {
		if err := s.RecordAnalysisMemberResult(id, target, MemberResult{
			Status: AnalysisSucceeded, Results: n, Counted: true,
			ByQuery: map[string]int{"actions/unpinned-tag": n},
		}); err != nil {
			t.Fatal(err)
		}
	}
	run, _ := s.AnalysisRun(id)
	for _, m := range run.Members {
		if m.Repository == "octo/app" && m.ByQuery["actions/unpinned-tag"] != 2 {
			t.Errorf("after a re-run the member credits %d, want the latest 2",
				m.ByQuery["actions/unpinned-tag"])
		}
	}
}
