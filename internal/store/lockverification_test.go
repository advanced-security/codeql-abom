package store

import (
	"testing"
	"time"
)

// Every request is a record of its own, the newest first, with what the tool said kept as it said
// it. A verification is evidence about one commit at one time, so none replaces another.
func TestEveryVerificationIsKeptWithWhatTheToolSaid(t *testing.T) {
	s := newTestStore(t)
	valid := true
	first := LockVerification{Repository: "octo/app", Commit: "c1", Mode: "network",
		Outcome: "warnings", ToolVersion: "v0.1.6", LockfileVersion: "v0.0.2", Valid: &valid,
		Findings: []LockVerificationFinding{{Workflow: ".github/workflows/ci.yml",
			Category: "ref-moved", Severity: "warning", Dependency: "actions/checkout@v4",
			Detail: "ref v4 now resolves to 11d5960a3267", Known: true}},
		RequestedBy: "cli", VerifiedAt: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)}
	second := LockVerification{Repository: "octo/app", Commit: "c1", Mode: "local",
		Outcome: "error", Error: "the tool exited 2 without a report", RequestedBy: "cli",
		VerifiedAt: time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)}
	for _, v := range []LockVerification{first, second} {
		if _, err := s.RecordLockVerification(v); err != nil {
			t.Fatalf("RecordLockVerification: %v", err)
		}
	}

	got, err := s.LockVerifications("OCTO/app", 10)
	if err != nil {
		t.Fatalf("LockVerifications: %v", err)
	}
	if len(got) != 2 || got[0].Mode != "local" || got[1].Mode != "network" {
		t.Fatalf("verifications = %+v, want both, newest first", got)
	}
	if got[0].Valid != nil || got[0].Findings == nil || len(got[0].Findings) != 0 {
		t.Errorf("an error run reads valid=%v findings=%v, want unknown and an empty list",
			got[0].Valid, got[0].Findings)
	}
	if got[1].Valid == nil || !*got[1].Valid || len(got[1].Findings) != 1 ||
		got[1].Findings[0].Category != "ref-moved" || !got[1].Findings[0].Known {
		t.Errorf("the network run reads %+v, want its finding kept", got[1])
	}

	last, ok, err := s.LastLockVerification("octo/app", "c1", "network")
	if err != nil || !ok || last.Outcome != "warnings" {
		t.Errorf("LastLockVerification = %+v, %v, %v; want the network run", last, ok, err)
	}
	n, err := s.LockVerificationsSince("network", time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC))
	if err != nil || n != 1 {
		t.Errorf("LockVerificationsSince = %d, %v; want 1", n, err)
	}
}

// The commit a request verifies is the one the repository resolves to now, and only where a
// lockfile was read there. No lockfile, or no reading at a current commit, leaves nothing to ask
// the tool about.
func TestTheCommitToVerifyIsTheCurrentOneWithALockfile(t *testing.T) {
	s := lockedCorpus(t)

	commit, ok, err := s.CurrentLockfileCommit("octo/cleave")
	if err != nil || !ok || commit != "c-cleave" {
		t.Errorf("octo/cleave: %q, %v, %v; want its current commit c-cleave", commit, ok, err)
	}
	for _, repository := range []string{"octo/unlocked", "octo/plain", "nobody/here"} {
		if commit, ok, err := s.CurrentLockfileCommit(repository); err != nil || ok {
			t.Errorf("%s: %q, %v, %v; want nothing to verify", repository, commit, ok, err)
		}
	}
	if commit, ok, err := s.CurrentLockfileCommit("octo/broken"); err != nil || !ok || commit != "c-broken" {
		t.Errorf("octo/broken: %q, %v, %v; want its commit: an unreadable lockfile is worth asking about", commit, ok, err)
	}
}
