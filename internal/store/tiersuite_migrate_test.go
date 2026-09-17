package store

import (
	"path/filepath"
	"testing"
)

// bindOldStyle writes a binding the way it was stored before suites were named by their pack.
func bindOldStyle(t *testing.T, s *Store, tier, suite string) {
	t.Helper()
	if _, err := s.DB().Exec(
		`INSERT INTO abom_tier_suite (tier, suite) VALUES (?, ?)`, tier, suite); err != nil {
		t.Fatalf("inserting the old binding: %v", err)
	}
	if _, err := s.DB().Exec(
		`INSERT OR REPLACE INTO abom_tier_configured (tier) VALUES (?)`, tier); err != nil {
		t.Fatalf("recording configured: %v", err)
	}
}

func reopen(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// Moving the suites into their own packs changed every stored binding's spelling. A crawl that
// came back bound to a path that no longer exists would analyze nothing and say nothing.
func TestABindingSurvivesTheMoveIntoPacks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	s := reopen(t, path)
	if _, err := s.DB().Exec(`DELETE FROM abom_tier_suite`); err != nil {
		t.Fatalf("clearing seeds: %v", err)
	}
	bindOldStyle(t, s, TierDirect, "ql/actions/abom/src/codeql-suites/abom-findings.qls")
	s.Close()

	got, err := reopen(t, path).TierSuites()
	if err != nil {
		t.Fatalf("TierSuites: %v", err)
	}

	want := "advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls"
	if len(got[TierDirect]) != 1 || got[TierDirect][0] != want {
		t.Errorf("direct = %v, want %q", got[TierDirect], want)
	}
}

// The migration must not reintroduce a suite somebody deliberately unbound, so it rewrites what
// is there rather than restoring a default.
func TestMigratingBindingsDoesNotRestoreWhatWasUnbound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	s := reopen(t, path)
	if err := s.SetTierSuites(TierDirect, nil); err != nil {
		t.Fatalf("SetTierSuites: %v", err)
	}
	s.Close()

	got, err := reopen(t, path).TierSuites()
	if err != nil {
		t.Fatalf("TierSuites: %v", err)
	}
	if len(got[TierDirect]) != 0 {
		t.Errorf("direct = %v, want the deliberate empty binding to survive", got[TierDirect])
	}
}

// A binding naming something the map does not know is left alone. Dropping it would silently
// change what a crawl runs; keeping it means the interface can show it and somebody can decide.
func TestAnUnrecognizedBindingIsLeftAloneRatherThanDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	s := reopen(t, path)
	if _, err := s.DB().Exec(`DELETE FROM abom_tier_suite`); err != nil {
		t.Fatalf("clearing seeds: %v", err)
	}
	bindOldStyle(t, s, TierTransitive, "some-org/their-pack:codeql-suites/theirs.qls")
	s.Close()

	got, err := reopen(t, path).TierSuites()
	if err != nil {
		t.Fatalf("TierSuites: %v", err)
	}
	want := "some-org/their-pack:codeql-suites/theirs.qls"
	if len(got[TierTransitive]) != 1 || got[TierTransitive][0] != want {
		t.Errorf("transitive = %v, want %q kept verbatim", got[TierTransitive], want)
	}
}

// Running the migration twice must not double a binding or undo the first pass.
func TestMigratingBindingsTwiceChangesNothingTheSecondTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	s := reopen(t, path)
	if _, err := s.DB().Exec(`DELETE FROM abom_tier_suite`); err != nil {
		t.Fatalf("clearing seeds: %v", err)
	}
	bindOldStyle(t, s, TierDirect, "ql/actions/abom/src/codeql-suites/abom-dependency-graph.qls")
	s.Close()

	first, err := reopen(t, path).TierSuites()
	if err != nil {
		t.Fatalf("TierSuites: %v", err)
	}
	second, err := reopen(t, path).TierSuites()
	if err != nil {
		t.Fatalf("TierSuites: %v", err)
	}

	if len(first[TierDirect]) != 1 || len(second[TierDirect]) != 1 {
		t.Fatalf("first %v, second %v; want one binding both times", first, second)
	}
	if first[TierDirect][0] != second[TierDirect][0] {
		t.Errorf("first %q, second %q", first[TierDirect][0], second[TierDirect][0])
	}
}
