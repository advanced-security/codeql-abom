package store

import "testing"

// ranAt records that a repository was analyzed at a given identity, which is what decides whether
// re-analyzing it would learn anything.
func ranAt(t *testing.T, s *Store, repo, pack, digest, cliVersion string) {
	t.Helper()
	if _, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "abom/edges", Repository: repo, DBCommit: "sha",
		Pack: pack, PackVersion: "1.0.0", PackDigest: digest, CLIVersion: cliVersion,
	}); err != nil {
		t.Fatalf("RecordQueryRun for %s: %v", repo, err)
	}
}

const (
	graphDigest    = "aaaa1111"
	findingsDigest = "bbbb2222"
	currentCLI     = "2.26.4"
)

func currentIdentity() []string { return []string{graphDigest, findingsDigest} }

// A whole-tier requeue re-clones every repository in the tier, and the checkout is the one cost
// the skip cannot avoid. Selecting only what is actually out of date avoids re-cloning everything.
func TestOnlyRepositoriesMissingTheCurrentIdentityAreRequeued(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "current", 1, "done")
	tierRow(t, s, "stale", 1, "done")
	ranAt(t, s, "octo/current", "graph", graphDigest, currentCLI)
	ranAt(t, s, "octo/current", "findings", findingsDigest, currentCLI)
	ranAt(t, s, "octo/stale", "graph", graphDigest, currentCLI)

	n, err := s.RequeueStale(currentCLI, currentIdentity())
	if err != nil {
		t.Fatalf("RequeueStale: %v", err)
	}
	if n != 1 {
		t.Errorf("requeued %d items, want only the one missing a pack", n)
	}
	if got := statusOfKey(t, s, "current"); got != "done" {
		t.Errorf("a repository already at the current identity was requeued (%q)", got)
	}
	if got := statusOfKey(t, s, "stale"); got != "pending" {
		t.Errorf("stale = %q, want pending", got)
	}
}

// A pack edit changes its digest, and that is the whole mechanism: the repository's recorded
// digest no longer matches, so it is out of date without anything else being consulted.
func TestAPackEditMakesEveryRepositoryStale(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "one", 1, "done")
	ranAt(t, s, "octo/one", "graph", graphDigest, currentCLI)
	ranAt(t, s, "octo/one", "findings", findingsDigest, currentCLI)

	n, err := s.RequeueStale(currentCLI, []string{"cccc3333", findingsDigest})
	if err != nil {
		t.Fatalf("RequeueStale: %v", err)
	}
	if n != 1 {
		t.Errorf("requeued %d, want the repository whose pack was edited", n)
	}
}

// The CLI version is inside the identity, so an upgrade invalidates the corpus. Selecting on the
// digest alone would silently keep results produced by a CodeQL nobody is running any more.
func TestACodeQLUpgradeMakesEveryRepositoryStale(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "one", 1, "done")
	ranAt(t, s, "octo/one", "graph", graphDigest, currentCLI)
	ranAt(t, s, "octo/one", "findings", findingsDigest, currentCLI)

	n, err := s.RequeueStale("2.27.0", currentIdentity())
	if err != nil {
		t.Fatalf("RequeueStale: %v", err)
	}
	if n != 1 {
		t.Errorf("requeued %d, want the repository analyzed by the older CodeQL", n)
	}
}

// Unavailable is left alone for the same reason a tier requeue leaves it: a checkout already
// known to fail is the one outcome re-running cannot improve.
func TestRequeueingWhatIsStaleLeavesUnfetchableDependenciesAlone(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "gone", 1, "unavailable")

	if _, err := s.RequeueStale(currentCLI, currentIdentity()); err != nil {
		t.Fatalf("RequeueStale: %v", err)
	}
	if got := statusOfKey(t, s, "gone"); got != "unavailable" {
		t.Errorf("gone = %q, want left unavailable", got)
	}
}

// Asking for nothing must not mean asking for everything: an empty identity is a caller that
// failed to resolve its packs.
func TestAnEmptyIdentityRequeuesNothing(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "one", 1, "done")

	n, err := s.RequeueStale(currentCLI, nil)
	if err == nil {
		t.Fatal("an empty identity was accepted")
	}
	if n != 0 {
		t.Errorf("requeued %d on a refused call", n)
	}
	if got := statusOfKey(t, s, "one"); got != "done" {
		t.Errorf("one = %q, want untouched", got)
	}
}

// A single dependency can be requeued without requeueing its whole tier.
func TestOneRepositoryCanBeRequeuedOnItsOwn(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "mine", 1, "done")
	tierRow(t, s, "theirs", 1, "done")

	n, err := s.RequeueRepository("octo/mine")
	if err != nil {
		t.Fatalf("RequeueRepository: %v", err)
	}
	if n != 1 {
		t.Errorf("requeued %d items, want 1", n)
	}
	if got := statusOfKey(t, s, "theirs"); got != "done" {
		t.Errorf("theirs = %q, want untouched", got)
	}
}
