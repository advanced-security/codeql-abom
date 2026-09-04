package store

import (
	"strings"
	"testing"
)

func TestActionsInventoryRanksByReach(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "actions/checkout", "v4", 10)
	seedDependency(t, s, "octo-org/app", "actions/cache", "v4", 12)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_blast_radius
			(node_row_id, dependent_repos, dependent_workflows, direct_dependents, max_depth)
		SELECT row_id, CASE node_key WHEN 'actions/checkout' THEN 50 ELSE 2 END, 1, 1, 1
		FROM abom_node WHERE is_external = 1`); err != nil {
		t.Fatalf("seeding blast radius: %v", err)
	}

	got, err := s.ActionsInventory("", 0)
	if err != nil {
		t.Fatalf("ActionsInventory: %v", err)
	}
	if got[0].NodeKey != "actions/checkout" {
		t.Errorf("first entry = %q, want the widest reach", got[0].NodeKey)
	}
	// The listing covers workflows as well as actions, so both kinds are expected here.
	var actions int
	for _, a := range got {
		if a.Kind == "action" {
			actions++
		}
	}
	if actions != 2 {
		t.Errorf("listed %d actions, want 2", actions)
	}
}

// The advisory count is a count of published statements about the repository, never a claim
// that the observed reference is affected.
func TestActionsInventoryCountsAdvisoriesWithoutJudging(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/init", "v3", 10)
	advisoryAgainst(t, s, "GHSA-inv", "high", "github/codeql-action", "3.0", "")

	got, err := s.ActionsInventory("", 0)
	if err != nil {
		t.Fatalf("ActionsInventory: %v", err)
	}
	if len(got) == 0 || got[0].Advisories != 1 {
		t.Fatalf("advisories = %+v, want the repository-scoped advisory counted", got)
	}
	// The refs are the evidence a reader needs alongside that count.
	if len(got[0].Refs) == 0 || !strings.Contains(got[0].Refs[0], "v3") {
		t.Errorf("refs = %v, want the observed reference", got[0].Refs)
	}
}

func TestActionsInventorySearchIsLiteral(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "actions/checkout", "v4", 10)
	seedDependency(t, s, "octo-org/app", "actions/cache", "v4", 12)

	got, err := s.ActionsInventory("checkout", 0)
	if err != nil {
		t.Fatalf("ActionsInventory: %v", err)
	}
	if len(got) != 1 || got[0].NodeKey != "actions/checkout" {
		t.Errorf("search returned %+v", got)
	}

	// A wildcard typed by a user is a literal, not a pattern.
	if wild, err := s.ActionsInventory("%", 0); err != nil {
		t.Fatalf("ActionsInventory: %v", err)
	} else if len(wild) != 0 {
		t.Errorf("a literal %% matched %d actions", len(wild))
	}
}

// Upstream repositories are not the organisation's to remediate, so listing them beside its own
// would imply ownership it does not have.
func TestRepositoriesOverviewExcludesUpstream(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "actions/checkout", "v4", 10)
	if _, err := s.InsertUpstreamRepository("third-party/tool"); err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}

	got, err := s.RepositoriesOverview("", 0)
	if err != nil {
		t.Fatalf("RepositoriesOverview: %v", err)
	}
	for _, r := range got {
		if r.FullName == "third-party/tool" {
			t.Error("an upstream repository was listed as under review")
		}
	}
	if len(got) != 1 {
		t.Errorf("listed %d repositories, want the one under review", len(got))
	}
}

func TestRepositoryDetailGroupsFindingsByRule(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "actions/checkout", "v4", 10)
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	if _, err := s.AddFindings([]Finding{
		{RuleID: "actions/unpinned-tag", RepositoryID: repoID,
			FilePath: ".github/workflows/ci.yml", StartLine: 10},
		{RuleID: "actions/unpinned-tag", RepositoryID: repoID,
			FilePath: ".github/workflows/ci.yml", StartLine: 20},
		{RuleID: "actions/missing-workflow-permissions", RepositoryID: repoID,
			FilePath: ".github/workflows/ci.yml", StartLine: 1},
	}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	got, err := s.RepositoryDetailOf("octo-org/app")
	if err != nil {
		t.Fatalf("RepositoryDetailOf: %v", err)
	}
	if got.Origin != OriginSeed {
		t.Errorf("Origin = %q", got.Origin)
	}
	if len(got.FindingsByRule) != 2 || got.FindingsByRule[0].RuleID != "actions/unpinned-tag" {
		t.Errorf("findings by rule = %+v, want the commonest rule first", got.FindingsByRule)
	}
	if len(got.Workflows) == 0 {
		t.Error("no workflows listed")
	}
}

func TestRepositoryDetailOfUnknownRepository(t *testing.T) {
	s := advisoryStore(t)
	if _, err := s.RepositoryDetailOf("nobody/nothing"); err == nil {
		t.Error("an unknown repository did not report an error")
	}
}
