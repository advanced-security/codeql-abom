package store

import "testing"

// Renewal is a comparison against what was last sent, so the ledger has to carry enough to make
// it. It first recorded only a package count, which cannot distinguish a version bump from no
// change at all.
func TestTheLedgerCarriesWhatWasSentNotJustHowMuch(t *testing.T) {
	s := newTestStore(t)

	if err := s.RecordSubmission(Submission{
		Repository: "advanced-security/codeql-abom-test1", Correlator: "abom-transitive-actions",
		Detector: "codeql-abom", PackageURLs: 2, PackageDigest: "abc123",
		Result: "SUCCESS", SubmittedAt: "2026-09-08T22:00:00Z",
	}); err != nil {
		t.Fatalf("RecordSubmission: %v", err)
	}

	got, err := s.LastSubmission("advanced-security/codeql-abom-test1")
	if err != nil {
		t.Fatalf("LastSubmission: %v", err)
	}
	if got.PackageDigest != "abc123" {
		t.Errorf("digest = %q, want the one submitted", got.PackageDigest)
	}
}

// A refused submission never reached GitHub, so nothing about the repository changed. Reporting it
// as the last submission would make a renewal check compare against a write that never happened.
func TestARefusedSubmissionIsNotTheLastSubmission(t *testing.T) {
	s := newTestStore(t)

	for _, r := range []Submission{
		{Repository: "acme/app", Correlator: "c", Detector: "d", PackageDigest: "real",
			Result: "SUCCESS", SubmittedAt: "2026-09-08T20:00:00Z"},
		{Repository: "acme/app", Correlator: "c", Detector: "d", PackageDigest: "never-sent",
			Result: "REFUSED", SubmittedAt: "2026-09-08T22:00:00Z"},
	} {
		if err := s.RecordSubmission(r); err != nil {
			t.Fatalf("RecordSubmission: %v", err)
		}
	}

	got, err := s.LastSubmission("acme/app")
	if err != nil {
		t.Fatalf("LastSubmission: %v", err)
	}
	if got.PackageDigest != "real" {
		t.Errorf("digest = %q; a refusal was treated as the last submission", got.PackageDigest)
	}
}

// A repository never submitted to has no last submission, and that is not the same as one whose
// supply chain is empty. Confusing them makes renewal either skip the first submission or repeat
// it forever.
func TestARepositoryNeverSubmittedToHasNoLastSubmission(t *testing.T) {
	s := newTestStore(t)

	got, err := s.LastSubmission("acme/never-touched")
	if err != nil {
		t.Fatalf("LastSubmission: %v", err)
	}
	if got.Submitted {
		t.Errorf("reported a submission that never happened: %+v", got)
	}
}

// Released means the path was deliberately handed back to GitHub's static analysis. A refusal is
// not that: nothing was written, and a reader who cannot tell the difference will believe a
// repository was released when it was never touched.
func TestARefusalIsNotRecordedAsARelease(t *testing.T) {
	s := newTestStore(t)

	if err := s.RecordSubmission(Submission{
		Repository: "advanced-security/dismiss-alerts", Correlator: "c", Detector: "d",
		PackageURLs: 0, Released: false, Result: "REFUSED",
		Message: "not an approved submission target", SubmittedAt: "2026-09-08T22:00:00Z",
	}); err != nil {
		t.Fatalf("RecordSubmission: %v", err)
	}

	got, err := s.Submissions()
	if err != nil {
		t.Fatalf("Submissions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("recorded %d rows, want 1", len(got))
	}
	if got[0].Released {
		t.Error("a refused submission is recorded as a release")
	}
}
