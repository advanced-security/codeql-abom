package store

import "testing"

// The default branch is a current fact and must be absent until somebody observes it. A gate that
// reads "main" from a repository nobody enumerated is acting on a guess.
func TestARepositoryNobodyEnumeratedHasNoDefaultBranch(t *testing.T) {
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/service")

	branch, known, err := s.DefaultBranch("acme/service")
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if known || branch != "" {
		t.Errorf("DefaultBranch = %q (known=%v), want nothing on record", branch, known)
	}
}

func TestTheDefaultBranchIsReadBackAsRecorded(t *testing.T) {
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/service")

	if err := s.SetDefaultBranch("acme/service", "trunk"); err != nil {
		t.Fatalf("SetDefaultBranch: %v", err)
	}

	branch, known, err := s.DefaultBranch("acme/service")
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if !known || branch != "trunk" {
		t.Errorf("DefaultBranch = %q (known=%v), want trunk", branch, known)
	}
}

// Absent is not unprotected. A repository nobody asked about and one that protects nothing are a
// coverage gap and a finding respectively, and a gate that reads the first as the second reports a
// policy as satisfied because it was never evaluated.
func TestARepositoryNobodyAskedAboutIsNotReportedAsUnprotected(t *testing.T) {
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/service")

	branches, asked, err := s.ProtectedBranches("acme/service")
	if err != nil {
		t.Fatalf("ProtectedBranches: %v", err)
	}
	if asked {
		t.Errorf("reported as asked with %v, want the question recorded as unasked", branches)
	}
}

func TestARepositoryThatProtectsNothingIsDistinguishableFromOneNobodyAsked(t *testing.T) {
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/service")

	if err := s.SetProtectedBranches("acme/service", nil); err != nil {
		t.Fatalf("SetProtectedBranches: %v", err)
	}

	branches, asked, err := s.ProtectedBranches("acme/service")
	if err != nil {
		t.Fatalf("ProtectedBranches: %v", err)
	}
	if !asked {
		t.Error("a repository that was asked about reports as unasked")
	}
	if len(branches) != 0 {
		t.Errorf("branches = %v, want none", branches)
	}
}

// Protection is removed as well as granted, so the whole answer replaces the previous one. Merging
// would leave a branch reported as protected long after it stopped being.
func TestUnprotectingABranchRemovesIt(t *testing.T) {
	s := newTestStore(t)
	seedOwnedRepo(t, s, "acme/service")

	if err := s.SetProtectedBranches("acme/service", []string{"main", "release"}); err != nil {
		t.Fatalf("SetProtectedBranches: %v", err)
	}
	if err := s.SetProtectedBranches("acme/service", []string{"main"}); err != nil {
		t.Fatalf("SetProtectedBranches (second): %v", err)
	}

	branches, _, err := s.ProtectedBranches("acme/service")
	if err != nil {
		t.Fatalf("ProtectedBranches: %v", err)
	}
	if len(branches) != 1 || branches[0] != "main" {
		t.Errorf("branches = %v, want only main", branches)
	}
}
