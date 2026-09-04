package store

import (
	"testing"
)

func TestSummaryOfEmptyDatabase(t *testing.T) {
	s := advisoryStore(t)

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got.Nodes != 0 || got.Edges != 0 || got.Findings != 0 || got.Advisories != 0 {
		t.Errorf("empty database summarized as %+v", got)
	}
	if got.Frontier == nil {
		t.Error("Frontier map is nil, which would panic a caller ranging over it")
	}
}

func TestSummaryCountsEachStage(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/init", "v3", 10)
	if _, err := s.InsertUpstreamRepository("third-party/tool"); err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}
	advisoryAgainst(t, s, "GHSA-summary", "high", "github/codeql-action", "3.0", "")
	exposeRepo(t, s, "octo-org/app", "GHSA-summary", "github/codeql-action/init", 1)

	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	if _, err := s.AddFindings([]Finding{{
		RuleID: "actions/unpinned-tag", RepositoryID: repoID,
		FilePath: ".github/workflows/ci.yml", StartLine: 10,
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}

	if got.SeedRepos != 1 {
		t.Errorf("SeedRepos = %d, want 1", got.SeedRepos)
	}
	if got.UpstreamRepos != 1 {
		t.Errorf("UpstreamRepos = %d, want 1", got.UpstreamRepos)
	}
	if got.Edges == 0 || got.Nodes == 0 {
		t.Errorf("graph counted as %d nodes / %d edges", got.Nodes, got.Edges)
	}
	if got.Findings != 1 || got.Rules != 1 {
		t.Errorf("findings = %d across %d rules, want 1/1", got.Findings, got.Rules)
	}
	if got.Advisories != 1 {
		t.Errorf("Advisories = %d, want 1", got.Advisories)
	}
	if got.AffectedActions != 1 {
		t.Errorf("AffectedActions = %d, want the one covered node", got.AffectedActions)
	}
	if got.Exposures != 1 || got.DirectExposures != 1 {
		t.Errorf("exposures = %d (%d direct), want 1/1", got.Exposures, got.DirectExposures)
	}
}

// A withdrawn advisory is not something to act on, so it must not inflate the count an operator
// reads as outstanding work.
func TestSummaryExcludesWithdrawnAdvisories(t *testing.T) {
	s := advisoryStore(t)
	advisoryAgainst(t, s, "GHSA-live", "high", "actions/checkout", "2.0", "")
	advisoryAgainst(t, s, "GHSA-gone", "high", "actions/cache", "2.0", "2026-01-01T00:00:00Z")

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got.Advisories != 1 {
		t.Errorf("Advisories = %d, want only the live one", got.Advisories)
	}
}

// Findings that attach to a node and those that do not are both real; the difference tells an
// operator whether the graph covers what the queries found.
func TestSummaryReportsLinkedFindingsSeparately(t *testing.T) {
	s := newTestStore(t)
	repoID := seedGraph(t, s, NewWriter(s, 0))

	if _, err := s.AddFindings([]Finding{
		{RuleID: "actions/unpinned-tag", RepositoryID: repoID,
			FilePath: ".github/workflows/main.yml", StartLine: 16},
		{RuleID: "actions/missing-workflow-permissions", RepositoryID: repoID,
			FilePath: "somewhere/else.yml", StartLine: 3},
	}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got.Findings != 2 {
		t.Errorf("Findings = %d, want 2", got.Findings)
	}
	if got.LinkedFindings != 1 {
		t.Errorf("LinkedFindings = %d, want 1: the other names no file in the graph",
			got.LinkedFindings)
	}
}

func TestSummaryCountsFrontierByStatus(t *testing.T) {
	s := advisoryStore(t)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, depth, status, enqueued_at)
		VALUES ('a/one', 1, 'pending', '2026-01-01'), ('b/two', 1, 'done', '2026-01-01'),
		       ('c/three', 1, 'done', '2026-01-01')`); err != nil {
		t.Fatalf("seeding frontier: %v", err)
	}

	got, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got.Frontier["pending"] != 1 || got.Frontier["done"] != 2 {
		t.Errorf("frontier = %v, want 1 pending and 2 done", got.Frontier)
	}
	if got.AnalyzedUpstreams != 2 {
		t.Errorf("AnalyzedUpstreams = %d, want 2", got.AnalyzedUpstreams)
	}
}
