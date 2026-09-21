package store

import "testing"

// A commit's lockfile is one file with one answer, so reading it twice is a correction rather
// than a second opinion. Accumulating rows would double every count the measurement rests on.
func TestReadingOneCommitTwiceReplacesItsCoverageRatherThanAddingToIt(t *testing.T) {
	s := newTestStore(t)
	first := []LockCoverage{
		{Coverage: "covered", Schema: "v0.0.2", Digest: "aaaa"},
		{WorkflowPath: ".github/workflows/build.yml", Coverage: "covered", Schema: "v0.0.2"},
		{WorkflowPath: ".github/workflows/old.yml", Coverage: "not_onboarded", Schema: "v0.0.2"},
	}
	if err := s.RecordLockCoverage("owner/repo", "sha1", first); err != nil {
		t.Fatalf("RecordLockCoverage: %v", err)
	}
	second := []LockCoverage{
		{Coverage: "covered", Schema: "v0.0.2", Digest: "bbbb"},
		{WorkflowPath: ".github/workflows/build.yml", Coverage: "covered", Schema: "v0.0.2"},
	}
	if err := s.RecordLockCoverage("owner/repo", "sha1", second); err != nil {
		t.Fatalf("RecordLockCoverage: %v", err)
	}

	counts, err := s.LockCoverageCounts()
	if err != nil {
		t.Fatalf("LockCoverageCounts: %v", err)
	}
	if got := counts["covered"]; got != 2 {
		t.Errorf("covered rows = %d, want 2: the second reading did not replace the first", got)
	}
	// The workflow that disappeared from the second reading must not survive it.
	if _, ok := counts["not_onboarded"]; ok {
		t.Error("a workflow absent from the current reading is still counted")
	}
}

// Absent is not zero. A state no row holds is missing from the result, so a caller can tell a
// state nothing reached from one this build never produces.
func TestACoverageStateNothingReachedIsAbsentRatherThanZero(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordLockCoverage("owner/repo", "sha1",
		[]LockCoverage{{Coverage: "not_onboarded"}}); err != nil {
		t.Fatalf("RecordLockCoverage: %v", err)
	}
	counts, err := s.LockCoverageCounts()
	if err != nil {
		t.Fatalf("LockCoverageCounts: %v", err)
	}
	if _, ok := counts["unsupported"]; ok {
		t.Error("a state nothing recorded is reported as present")
	}
	if counts["not_onboarded"] != 1 {
		t.Errorf("not_onboarded = %d, want 1", counts["not_onboarded"])
	}
}

// Two repositories at the same commit id are different evidence, and one must not clear the
// other. Forks make this reachable rather than hypothetical.
func TestRecordingOneRepositoryDoesNotClearAnotherAtTheSameCommit(t *testing.T) {
	s := newTestStore(t)
	rows := []LockCoverage{{Coverage: "covered", Schema: "v0.0.2"}}
	if err := s.RecordLockCoverage("owner/one", "sha1", rows); err != nil {
		t.Fatalf("RecordLockCoverage: %v", err)
	}
	if err := s.RecordLockCoverage("owner/two", "sha1", rows); err != nil {
		t.Fatalf("RecordLockCoverage: %v", err)
	}
	counts, err := s.LockCoverageCounts()
	if err != nil {
		t.Fatalf("LockCoverageCounts: %v", err)
	}
	if counts["covered"] != 2 {
		t.Errorf("covered rows = %d, want 2", counts["covered"])
	}
}

// The reason an unsupported lockfile gives is the only thing that can report it, so it has to
// survive the round trip rather than being reconstructed from the state name.
func TestAnUnsupportedLockfileKeepsItsReasonAndTheSchemaThatCausedIt(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordLockCoverage("owner/repo", "sha1", []LockCoverage{{
		Coverage: "unsupported",
		Schema:   "future-version",
		Digest:   "cccc",
		Reason:   "lockfile version is newer than this binary supports",
	}}); err != nil {
		t.Fatalf("RecordLockCoverage: %v", err)
	}
	var schema, reason, digest string
	err := s.DB().QueryRow(`
		SELECT schema_version, reason, lock_digest FROM abom_lock_coverage
		WHERE repository = ? AND commit_sha = ? AND workflow_path = ''`,
		"owner/repo", "sha1").Scan(&schema, &reason, &digest)
	if err != nil {
		t.Fatalf("reading the recorded row: %v", err)
	}
	if schema != "future-version" {
		t.Errorf("schema = %q, want %q", schema, "future-version")
	}
	if reason == "" {
		t.Error("the reason did not survive; nothing downstream can say why coverage failed")
	}
	if digest != "cccc" {
		t.Errorf("digest = %q, want %q", digest, "cccc")
	}
}
