package store

import (
	"strings"
	"testing"
)

// A row that has been analyzed should name what analyzed it, because "no findings" means nothing
// until you know whether anything looked. The suites come from the run that was recorded against
// the commit, never from the tier's current binding: the binding is what happens next, and
// showing it would make a suite enabled this morning look retroactively applied.
func TestAnAnalyzedRowNamesTheSuitesThatProducedIt(t *testing.T) {
	s := newTestStore(t)
	seedAnalyzedRow(t, s, "actions/checkout", "v4", "actions", "checkout", "cafe1234")
	for _, q := range []QueryRun{
		{AnalysisMode: AnalysisModeRegular,
			QueryID: "abom/edges", Pack: "advanced-security/abom-actions-graph-extraction-src",
			PackVersion: "2.26.4", CLIVersion: "2.26.4",
			Repository: "actions/checkout", DBCommit: "cafe1234"},
		{AnalysisMode: AnalysisModeRegular,
			QueryID: "actions/unpinned-tag", Pack: "codeql/actions-queries",
			PackVersion: "0.6.34", CLIVersion: "2.26.4",
			Repository: "actions/checkout", DBCommit: "cafe1234"},
	} {
		if _, err := s.RecordQueryRun(q); err != nil {
			t.Fatalf("RecordQueryRun(%s): %v", q.QueryID, err)
		}
	}

	item := onlyFrontierItem(t, s)
	if len(item.Suites) != 2 {
		t.Fatalf("want both suites, got %v", item.Suites)
	}
	joined := strings.Join(item.Suites, " ")
	for _, want := range []string{
		"advanced-security/abom-actions-graph-extraction-src@2.26.4",
		"codeql/actions-queries@0.6.34",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("want %q named, got %v", want, item.Suites)
		}
	}
	if item.CLIVersion != "2.26.4" {
		t.Errorf("want the CodeQL that ran, got %q", item.CLIVersion)
	}
}

// 1,555 of 1,707 runs on the real corpus were recorded before pack identity existed, so most
// analyzed rows can name nothing. That is *not known*, and must stay distinguishable from a row
// analyzed with no suites at all — the same rule that stops an unmeasured node being drawn at the
// smallest radius.
func TestARowAnalyzedBeforePackIdentityNamesNothingRatherThanClaimingNoSuites(t *testing.T) {
	s := newTestStore(t)
	seedAnalyzedRow(t, s, "actions/cache", "", "actions", "cache", "beef5678")
	if _, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "abom/edges", Repository: "actions/cache", DBCommit: "beef5678",
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	item := onlyFrontierItem(t, s)
	if len(item.Suites) != 0 {
		t.Fatalf("a run with no pack recorded cannot name one, got %v", item.Suites)
	}
	if item.SuitesKnown {
		t.Error("suite provenance is absent here, and absent must not read as a measurement")
	}
}

// The join is repository plus commit, so a run against a different checkout of the same
// repository is a different analysis and must not be attributed to this row.
func TestSuitesDoNotLeakFromAnotherCommitOfTheSameRepository(t *testing.T) {
	s := newTestStore(t)
	seedAnalyzedRow(t, s, "actions/checkout", "v4", "actions", "checkout", "cafe1234")
	if _, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "actions/unpinned-tag", Pack: "codeql/actions-queries", PackVersion: "0.6.34",
		CLIVersion: "2.26.4", Repository: "actions/checkout", DBCommit: "0ther999",
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	item := onlyFrontierItem(t, s)
	if len(item.Suites) != 0 {
		t.Fatalf("another commit's run was attributed to this row: %v", item.Suites)
	}
}

// An unanalyzed row has no commit, and an empty commit must not match every other run that also
// lacks one. This is the join degenerating into "everything", which is how a queued dependency
// would come to claim it had been analyzed with something.
func TestARowWithNoCommitMatchesNoRunAtAll(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES ('actions/stale', '', 'actions', 'stale', 1, 'pending', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}
	if _, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "abom/edges", Pack: "codeql/actions-queries", PackVersion: "0.6.34",
		Repository: "actions/stale",
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	item := onlyFrontierItem(t, s)
	if len(item.Suites) != 0 || item.SuitesKnown {
		t.Fatalf("a row with no commit joined to a run with no commit: %v", item.Suites)
	}
}

// A row settled because its content matched an earlier commit was never run at its own
// `resolved_sha`, so a strict commit join reports it as unrecorded. It is not: the same database
// was analyzed, at a commit this repository's own content table names. Answering "not known"
// here would invent a coverage gap out of an optimization, which is the mirror of claiming
// coverage that was never earned.
func TestARowSettledByEquivalentContentNamesTheSuitesThatRan(t *testing.T) {
	s := newTestStore(t)
	seedAnalyzedRow(t, s, "actions/checkout", "v4", "actions", "checkout", "new1234")
	sameContent(t, s, "actions/checkout", "old9999", "digest-a")
	sameContent(t, s, "actions/checkout", "new1234", "digest-a")
	if _, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "actions/unpinned-tag", Pack: "codeql/actions-queries", PackVersion: "0.6.34",
		CLIVersion: "2.26.4", Repository: "actions/checkout", DBCommit: "old9999",
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	item := onlyFrontierItem(t, s)
	if !item.SuitesKnown {
		t.Fatal("an equivalent database was analyzed, so the row is not unrecorded")
	}
	if len(item.Suites) != 1 || !strings.Contains(item.Suites[0], "codeql/actions-queries") {
		t.Errorf("want the suite that ran, got %v", item.Suites)
	}
}

// Equivalence is the whole justification, so content that differs must join to nothing — the
// same rule as the commit join it stands in for.
func TestSuitesDoNotLeakFromACommitWhoseContentDiffers(t *testing.T) {
	s := newTestStore(t)
	seedAnalyzedRow(t, s, "actions/checkout", "v4", "actions", "checkout", "new1234")
	sameContent(t, s, "actions/checkout", "old9999", "digest-a")
	sameContent(t, s, "actions/checkout", "new1234", "digest-b")
	if _, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "actions/unpinned-tag", Pack: "codeql/actions-queries", PackVersion: "0.6.34",
		CLIVersion: "2.26.4", Repository: "actions/checkout", DBCommit: "old9999",
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	item := onlyFrontierItem(t, s)
	if len(item.Suites) != 0 || item.SuitesKnown {
		t.Fatalf("a run against different content was attributed to this row: %v", item.Suites)
	}
}

func sameContent(t *testing.T, s *Store, repository, commit, digest string) {
	t.Helper()
	if err := s.RecordCommitContent(repository, commit, digest); err != nil {
		t.Fatalf("RecordCommitContent(%s): %v", commit, err)
	}
}

func seedAnalyzedRow(t *testing.T, s *Store, nodeKey, ref, owner, repo, sha string) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier
			(node_key, ref, owner, repo, depth, status, enqueued_at, resolved_sha, analyzed_at)
		VALUES (?, ?, ?, ?, 1, 'done', '2026-01-01T00:00:00Z', ?, '2026-01-02T00:00:00Z')`,
		nodeKey, ref, owner, repo, sha); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}
}

func onlyFrontierItem(t *testing.T, s *Store) FrontierItem {
	t.Helper()
	items, err := s.FrontierSnapshot("", 0)
	if err != nil {
		t.Fatalf("FrontierSnapshot: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want one row, got %d", len(items))
	}
	return items[0]
}
