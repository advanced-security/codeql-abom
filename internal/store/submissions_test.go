package store

import "testing"

// A submission cannot be withdrawn and there is no delete endpoint, so the only way to find an
// artifact left on somebody's repository is to have written down that it was made.
func TestASubmissionIsRecordedSoItCanBeFoundAndSuperseded(t *testing.T) {
	s := newTestStore(t)

	rec := Submission{
		Repository:  "advanced-security/codeql-abom-test1",
		SHA:         "5f2a2d7425ce7567773b0d95894d961c8c659f07",
		Correlator:  "abom-transitive-actions",
		Detector:    "codeql-abom",
		Manifest:    ".abom/transitive-dependencies.json",
		PackageURLs: 3,
		Result:      "SUCCESS",
		SubmittedAt: "2026-09-08T22:00:00Z",
	}
	if err := s.RecordSubmission(rec); err != nil {
		t.Fatalf("RecordSubmission: %v", err)
	}

	got, err := s.Submissions()
	if err != nil {
		t.Fatalf("Submissions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("recorded %d submissions, want 1", len(got))
	}
	if got[0].Repository != rec.Repository || got[0].Correlator != rec.Correlator {
		t.Errorf("recorded %+v, want %+v", got[0], rec)
	}
}

// Superseding is a second write to the same correlator, not an edit. Losing the earlier row would
// erase the evidence of what was on the repository before, which is the only way to tell whether
// a previous submission has actually been cleaned up.
func TestSupersedingASubmissionKeepsBothRows(t *testing.T) {
	s := newTestStore(t)

	base := Submission{
		Repository: "advanced-security/codeql-abom-test1", Correlator: "abom-transitive-actions",
		Detector: "codeql-abom", Manifest: ".abom/transitive-dependencies.json", Result: "SUCCESS",
	}
	first := base
	first.SHA, first.PackageURLs, first.SubmittedAt = "aaa", 3, "2026-09-08T22:00:00Z"
	second := base
	second.SHA, second.PackageURLs, second.SubmittedAt = "bbb", 0, "2026-09-08T23:00:00Z"

	for _, r := range []Submission{first, second} {
		if err := s.RecordSubmission(r); err != nil {
			t.Fatalf("RecordSubmission: %v", err)
		}
	}

	got, err := s.Submissions()
	if err != nil {
		t.Fatalf("Submissions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("kept %d rows, want both", len(got))
	}
	// Newest first, so the current state of a repository is the row you read.
	if got[0].SubmittedAt != "2026-09-08T23:00:00Z" {
		t.Errorf("first row is %s, want the newest", got[0].SubmittedAt)
	}
}

// Zero submitted packages is a release: it hands the path back to GitHub's static analysis. It
// has to be distinguishable from a submission that was never made, which is the absent-is-not-zero
// rule at the submission boundary.
func TestAReleaseIsRecordedAsASubmissionOfNothingRatherThanAsNoRow(t *testing.T) {
	s := newTestStore(t)

	if err := s.RecordSubmission(Submission{
		Repository: "advanced-security/codeql-abom-test1", Correlator: "abom-transitive-actions",
		Detector: "codeql-abom", PackageURLs: 0, Released: true, Result: "SUCCESS",
		SubmittedAt: "2026-09-08T22:00:00Z",
	}); err != nil {
		t.Fatalf("RecordSubmission: %v", err)
	}

	got, err := s.Submissions()
	if err != nil {
		t.Fatalf("Submissions: %v", err)
	}
	if len(got) != 1 || !got[0].Released {
		t.Fatalf("release not recorded as such: %+v", got)
	}
}
