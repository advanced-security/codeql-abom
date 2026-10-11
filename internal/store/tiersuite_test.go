package store

import (
	"path/filepath"
	"testing"
)

func suiteStore(t *testing.T) *Store {
	t.Helper()
	s, err := openTestStore(t, filepath.Join(t.TempDir(), "abom.db"), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// The whole point of the binding is that what runs is configuration rather than a flag, so a
// fresh database has to answer the question before anyone edits anything.
func TestAFreshDatabaseAlreadyKnowsWhatEachTierRuns(t *testing.T) {
	bound, err := suiteStore(t).TierSuites()
	if err != nil {
		t.Fatalf("TierSuites: %v", err)
	}

	for _, tier := range []string{TierDirect, TierTransitive} {
		if len(bound[tier]) == 0 {
			t.Errorf("%s tier binds to no suite, so a crawl would run nothing for it", tier)
		}
	}
}

// Volume is the reason the tiers differ at all: a transitive dependency arrives through someone
// else's intermediary and there are far more of them, so it earns the cheaper pass.
func TestTheTransitiveTierRunsNoMoreThanTheDirectOne(t *testing.T) {
	bound, err := suiteStore(t).TierSuites()
	if err != nil {
		t.Fatalf("TierSuites: %v", err)
	}

	if len(bound[TierTransitive]) > len(bound[TierDirect]) {
		t.Errorf("transitive runs %d suites and direct runs %d, which inverts the cost argument",
			len(bound[TierTransitive]), len(bound[TierDirect]))
	}
}

// "Editable at run time" is the requirement. A binding that needed a rebuild would be the flag
// it replaced.
func TestABindingSurvivesBeingRewritten(t *testing.T) {
	s := suiteStore(t)

	if err := s.SetTierSuites(TierTransitive, []string{"a.qls", "b.qls"}); err != nil {
		t.Fatalf("SetTierSuites: %v", err)
	}
	bound, err := s.TierSuites()
	if err != nil {
		t.Fatalf("TierSuites: %v", err)
	}

	if got := bound[TierTransitive]; len(got) != 2 || got[0] != "a.qls" || got[1] != "b.qls" {
		t.Errorf("got %v, want the two suites just bound", got)
	}
}

// Rewriting one tier must not disturb another, or editing the cheap pass would silently change
// the expensive one.
func TestRewritingOneTierLeavesTheOtherAlone(t *testing.T) {
	s := suiteStore(t)
	before, err := s.TierSuites()
	if err != nil {
		t.Fatalf("TierSuites: %v", err)
	}

	if err := s.SetTierSuites(TierTransitive, []string{"only.qls"}); err != nil {
		t.Fatalf("SetTierSuites: %v", err)
	}
	after, err := s.TierSuites()
	if err != nil {
		t.Fatalf("TierSuites: %v", err)
	}

	if len(after[TierDirect]) != len(before[TierDirect]) {
		t.Errorf("direct went from %v to %v", before[TierDirect], after[TierDirect])
	}
}

// Binding nothing is a legitimate instruction — "do not analyze this tier" — and has to be
// distinguishable from never having configured it, or the defaults would come back unasked.
func TestATierCanBeBoundToNothingAtAll(t *testing.T) {
	s := suiteStore(t)

	if err := s.SetTierSuites(TierTransitive, nil); err != nil {
		t.Fatalf("SetTierSuites: %v", err)
	}
	bound, err := s.TierSuites()
	if err != nil {
		t.Fatalf("TierSuites: %v", err)
	}

	if len(bound[TierTransitive]) != 0 {
		t.Errorf("got %v, want the tier to stay empty rather than fall back to a default",
			bound[TierTransitive])
	}
}

// A tier that is not a tier would bind work to nothing, so it is refused where it is named
// rather than discovered as an empty crawl later.
func TestAnUnknownTierIsRefused(t *testing.T) {
	if err := suiteStore(t).SetTierSuites("everything", []string{"a.qls"}); err == nil {
		t.Error("binding an unknown tier was accepted")
	}
}
