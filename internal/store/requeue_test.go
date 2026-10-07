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

// A new CodeQL or pack cannot find Actions source a repository does not hold, and no run is on
// record for it to be stale against.
func TestRequeueingWhatIsStaleLeavesNothingToAnalyzeAlone(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "docs", 0, "nothing")
	tierRow(t, s, "stale", 1, "done")

	n, err := s.RequeueStale(currentCLI, currentIdentity())
	if err != nil {
		t.Fatalf("RequeueStale: %v", err)
	}
	if n != 1 || statusOfKey(t, s, "stale") != "pending" {
		t.Fatalf("requeued %d, want the stale analysis requeued", n)
	}
	if got := statusOfKey(t, s, "docs"); got != "nothing" {
		t.Errorf("docs = %q, want nothing to analyze left alone", got)
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

// Re-analyzing one repository the operator names reads it again whatever it last held, including
// nothing to analyze: a workflow added without an event is found that way.
func TestOneRepositoryWithNothingToAnalyzeIsReadAgainOnRequest(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "mine", 0, "nothing")

	n, err := s.RequeueRepository("octo/mine")
	if err != nil {
		t.Fatalf("RequeueRepository: %v", err)
	}
	if n != 1 || statusOfKey(t, s, "mine") != "pending" {
		t.Errorf("requeued %d, status %q; want it read again", n, statusOfKey(t, s, "mine"))
	}
}

// A whole tier is re-analyzed because what analyzes it changed, which reading nothing again
// cannot answer: it would cost a clone and find nothing.
func TestATierRequeueLeavesNothingToAnalyzeAlone(t *testing.T) {
	s := newTestStore(t)
	declareOwner(t, s, "octo")
	tierRow(t, s, "mine", 0, "nothing")
	tierRow(t, s, "analyzed", 0, "done")
	n, err := s.RequeueTier(TierOwned)
	if err != nil {
		t.Fatalf("RequeueTier: %v", err)
	}
	if n != 1 || statusOfKey(t, s, "analyzed") != "pending" {
		t.Fatalf("requeued %d, want the analyzed repository requeued", n)
	}
	if got := statusOfKey(t, s, "mine"); got != "nothing" {
		t.Errorf("status %q, want nothing to analyze left alone", got)
	}
}

// unavailableRow records a dependency the way Unavailable leaves it: out of attempts, with the
// reason the checkout failed.
func unavailableRow(t *testing.T, s *Store, key, owner, repo string) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, owner, repo, depth, status, score, enqueued_at,
			attempts, completed_at, reason)
		VALUES (?, ?, ?, 1, 'unavailable', 1, '2026-01-01T00:00:00Z', 3,
			'2026-01-02T00:00:00Z', 'fatal: could not read Username')`,
		key, owner, repo); err != nil {
		t.Fatalf("inserting %s: %v", key, err)
	}
}

// A private repository fetched before Git had a credential is recorded as unavailable. Fixing
// the credential has to be able to heal that without starting a new database.
func TestRetryingUnavailableWorkPutsItBackWithACleanSlate(t *testing.T) {
	s := newTestStore(t)
	unavailableRow(t, s, "octo/private", "octo", "private")

	got, err := s.RetryUnavailable()
	if err != nil {
		t.Fatalf("RetryUnavailable: %v", err)
	}
	if got.Requeued != 1 || got.NamesNoRepository != 0 {
		t.Errorf("retry = %+v, want the one repository put back", got)
	}

	var status string
	var attempts int
	var reason, next, claimed any
	if err := s.DB().QueryRow(`
		SELECT status, attempts, reason, next_attempt_at, claimed_at FROM abom_frontier
		WHERE node_key = 'octo/private'`).
		Scan(&status, &attempts, &reason, &next, &claimed); err != nil {
		t.Fatalf("reading item: %v", err)
	}
	if status != "pending" || attempts != 0 || reason != nil || next != nil || claimed != nil {
		t.Errorf("status=%q attempts=%d reason=%v next=%v claimed=%v, want a fresh pending "+
			"row: a spent budget would fail it again at once, a kept reason would describe "+
			"a failure that is no longer current", status, attempts, reason, next, claimed)
	}
}

// A reference assembled from an expression can never be fetched, so retrying it would only
// rediscover that. It stays unavailable, and the count says so rather than hiding it.
func TestRetryingLeavesAReferenceThatNamesNoRepositoryUnavailable(t *testing.T) {
	s := newTestStore(t)
	unavailableRow(t, s, "${{ github.repository }}/setup", "${{ github.repository }}", "setup")
	unavailableRow(t, s, "octo/private", "octo", "private")

	got, err := s.RetryUnavailable()
	if err != nil {
		t.Fatalf("RetryUnavailable: %v", err)
	}
	if got.Requeued != 1 || got.NamesNoRepository != 1 {
		t.Errorf("retry = %+v, want 1 put back and 1 that names no repository", got)
	}
	if status := statusOfKey(t, s, "${{ github.repository }}/setup"); status != "unavailable" {
		t.Errorf("expression reference = %q, want left unavailable", status)
	}
}

// Retrying what could not be fetched is not re-analysis: finished work is left as it is.
func TestRetryingUnavailableWorkLeavesFinishedWorkAlone(t *testing.T) {
	s := newTestStore(t)
	tierRow(t, s, "finished", 1, "done")

	got, err := s.RetryUnavailable()
	if err != nil {
		t.Fatalf("RetryUnavailable: %v", err)
	}
	if got.Requeued != 0 {
		t.Errorf("requeued %d, want nothing when nothing is unavailable", got.Requeued)
	}
	if status := statusOfKey(t, s, "finished"); status != "done" {
		t.Errorf("finished = %q, want untouched", status)
	}
}

// Naming one repository is how an operator says "I fixed access to this one", so it retries that
// repository when it was unavailable, and leaves every other unavailable row as it was.
func TestRequeueingARepositoryRetriesItWhenItWasUnavailable(t *testing.T) {
	s := newTestStore(t)
	unavailableRow(t, s, "octo/mine", "octo", "mine")
	unavailableRow(t, s, "octo/theirs", "octo", "theirs")

	n, err := s.RequeueRepository("octo/mine")
	if err != nil {
		t.Fatalf("RequeueRepository: %v", err)
	}
	if n != 1 {
		t.Errorf("requeued %d items, want 1", n)
	}
	if got := statusOfKey(t, s, "octo/mine"); got != "pending" {
		t.Errorf("mine = %q, want pending", got)
	}
	if got := statusOfKey(t, s, "octo/theirs"); got != "unavailable" {
		t.Errorf("theirs = %q, want left unavailable", got)
	}
}
