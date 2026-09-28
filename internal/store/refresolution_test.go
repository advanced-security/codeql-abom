package store

import (
	"testing"
)

// A pinned SHA carries no version, and GitHub generates Dependabot alerts for actions "that use
// semantic versioning, not SHA versioning". SHA-pinned edges are un-alertable until the SHA can be
// named.
//
// What makes that safe is provenance. A tag that the crawl watched resolve to a SHA is a
// measurement; a trailing `# v4.2.2` comment is a claim somebody typed, and submitting a version
// on a claim would put a reference into a monitored organization's graph that nothing verified.

func TestATagTheCrawlResolvedIsFoundInBothDirections(t *testing.T) {
	s := newTestStore(t)
	record(t, s, RefResolution{
		Owner: "actions", Repo: "checkout", Tag: "v4.2.2",
		CommitSHA: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678", Source: ResolutionCrawl,
	})

	if got, ok := semverFor(t, s, "actions", "checkout", "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"); !ok || got != "v4.2.2" {
		t.Errorf("SemverForSHA = %q, %v; want v4.2.2", got, ok)
	}
	if got, ok := shaFor(t, s, "actions", "checkout", "v4.2.2"); !ok || got != "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678" {
		t.Errorf("SHAForTag = %q, %v; want the recorded commit", got, ok)
	}
}

// The invariant this table exists to protect. A comment is evidence of intent, not of fact.
func TestAPinCommentAloneDoesNotNameAVersionForSubmission(t *testing.T) {
	s := newTestStore(t)
	record(t, s, RefResolution{
		Owner: "actions", Repo: "checkout", Tag: "v4.2.2",
		CommitSHA: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678", Source: ResolutionComment,
	})

	if got, ok := semverFor(t, s, "actions", "checkout", "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"); ok {
		t.Errorf("SemverForSHA = %q on a comment alone; a claim must not become a submitted version", got)
	}
}

// And a claim must never displace a measurement, whichever order they arrive in.
func TestAClaimDoesNotDisplaceAMeasurement(t *testing.T) {
	s := newTestStore(t)
	verified := RefResolution{
		Owner: "actions", Repo: "checkout", Tag: "v4.2.2",
		CommitSHA: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678", Source: ResolutionCrawl,
	}
	record(t, s, verified)
	claim := verified
	claim.Source = ResolutionComment
	record(t, s, claim)

	if got, ok := semverFor(t, s, "actions", "checkout", verified.CommitSHA); !ok || got != "v4.2.2" {
		t.Errorf("SemverForSHA = %q, %v after a claim arrived over a measurement; want v4.2.2", got, ok)
	}
}

// A measurement arriving after a claim promotes the row, or the corpus would stay un-alertable
// for every reference whose comment was seen first.
func TestAMeasurementPromotesAnExistingClaim(t *testing.T) {
	s := newTestStore(t)
	claim := RefResolution{
		Owner: "actions", Repo: "checkout", Tag: "v4.2.2",
		CommitSHA: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678", Source: ResolutionComment,
	}
	record(t, s, claim)
	verified := claim
	verified.Source = ResolutionCrawl
	record(t, s, verified)

	if got, ok := semverFor(t, s, "actions", "checkout", claim.CommitSHA); !ok || got != "v4.2.2" {
		t.Errorf("SemverForSHA = %q, %v; a measurement must promote the claim", got, ok)
	}
}

// Releases tag one commit several times: v4, v4.2 and v4.2.2 can all point at it. The purl wants
// the one that says the most, because `4.%2A.%2A` is a range and `4.2.2` is a version.
func TestTheMostSpecificTagIsChosenWhenACommitCarriesSeveral(t *testing.T) {
	s := newTestStore(t)
	sha := "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
	for _, tag := range []string{"v4", "v4.2", "v4.2.2"} {
		record(t, s, RefResolution{
			Owner: "actions", Repo: "checkout", Tag: tag, CommitSHA: sha, Source: ResolutionCrawl,
		})
	}

	if got, ok := semverFor(t, s, "actions", "checkout", sha); !ok || got != "v4.2.2" {
		t.Errorf("SemverForSHA = %q, %v; want the most specific tag", got, ok)
	}
}

// A moving tag is the reason a resolution carries a time. Both readings are kept, and the newest
// is what the tag means now.
func TestAMovingTagKeepsItsHistoryAndReportsTheNewest(t *testing.T) {
	s := newTestStore(t)
	older := RefResolution{
		Owner: "actions", Repo: "checkout", Tag: "v4",
		CommitSHA: "1111111111111111111111111111111111111111", Source: ResolutionCrawl,
		ResolvedAt: "2026-01-01T00:00:00Z",
	}
	newer := older
	newer.CommitSHA = "2222222222222222222222222222222222222222"
	newer.ResolvedAt = "2026-06-01T00:00:00Z"
	record(t, s, older)
	record(t, s, newer)

	if got, ok := shaFor(t, s, "actions", "checkout", "v4"); !ok || got != newer.CommitSHA {
		t.Errorf("SHAForTag = %q, %v; a moved tag reports where it points now", got, ok)
	}
	// The older reading is still readable, or nothing can explain a purl submitted last month.
	if got, ok := semverFor(t, s, "actions", "checkout", older.CommitSHA); !ok || got != "v4" {
		t.Errorf("SemverForSHA on the superseded commit = %q, %v; the history must survive", got, ok)
	}
}

// The crawl already resolves a tag to a commit and throws the pair away.
func TestBackfillDerivesResolutionsFromWhatTheCrawlAlreadyResolved(t *testing.T) {
	s := newTestStore(t)
	seedFrontierResolution(t, s, "actions", "checkout", "v4.2.2",
		"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678")
	// A branch is not a version, so it contributes nothing.
	seedFrontierResolution(t, s, "actions", "setup-node", "main",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	n, err := s.BackfillRefResolutions()
	if err != nil {
		t.Fatalf("BackfillRefResolutions: %v", err)
	}
	if n != 1 {
		t.Errorf("backfilled %d, want 1: only a semantic tag resolves to a version", n)
	}
	if got, ok := semverFor(t, s, "actions", "checkout", "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"); !ok || got != "v4.2.2" {
		t.Errorf("SemverForSHA = %q, %v after backfill", got, ok)
	}
	if _, ok := semverFor(t, s, "actions", "setup-node", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); ok {
		t.Error("a branch was recorded as a version")
	}
}

// Backfilling twice must not multiply rows, because it runs after every crawl.
func TestBackfillIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	seedFrontierResolution(t, s, "actions", "checkout", "v4.2.2",
		"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678")

	if _, err := s.BackfillRefResolutions(); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	again, err := s.BackfillRefResolutions()
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if again != 0 {
		t.Errorf("second backfill recorded %d, want 0", again)
	}
}

func record(t *testing.T, s *Store, r RefResolution) {
	t.Helper()
	if err := s.RecordRefResolution(r); err != nil {
		t.Fatalf("RecordRefResolution(%+v): %v", r, err)
	}
}

func semverFor(t *testing.T, s *Store, owner, repo, sha string) (string, bool) {
	t.Helper()
	got, ok, err := s.SemverForSHA(owner, repo, sha)
	if err != nil {
		t.Fatalf("SemverForSHA: %v", err)
	}
	return got, ok
}

func shaFor(t *testing.T, s *Store, owner, repo, tag string) (string, bool) {
	t.Helper()
	got, ok, err := s.SHAForTag(owner, repo, tag)
	if err != nil {
		t.Fatalf("SHAForTag: %v", err)
	}
	return got, ok
}

func seedFrontierResolution(t *testing.T, s *Store, owner, repo, ref, sha string) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, score,
			enqueued_at, analysis_count, resolved_sha)
		VALUES (?, ?, ?, ?, 1, 'done', 1, '2026-01-01T00:00:00Z', 1, ?)`,
		owner+"/"+repo, ref, owner, repo, sha); err != nil {
		t.Fatalf("seeding the frontier for %s/%s@%s: %v", owner, repo, ref, err)
	}
}
