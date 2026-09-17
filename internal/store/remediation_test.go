package store

import (
	"testing"
)

// exposeRepo records that a repository reaches an affected action, as BuildExposure would.
func exposeRepo(t *testing.T, s *Store, repo, ghsa, nodeKey string, hops int) {
	t.Helper()
	repoID, err := s.UpsertRepository(repo, "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	var nodeID int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM abom_node WHERE node_key = ?`, nodeKey).Scan(&nodeID); err != nil {
		t.Fatalf("resolving %s: %v", nodeKey, err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_exposure (repository_row_id, node_row_id, ghsa_id, hops, path)
		VALUES (?, ?, ?, ?, '[]')
		ON CONFLICT(repository_row_id, node_row_id, ghsa_id) DO UPDATE SET hops = excluded.hops`,
		repoID, nodeID, ghsa, hops); err != nil {
		t.Fatalf("recording exposure: %v", err)
	}
}

// advisoryAgainst records an advisory covering a package, optionally with a published fix.
func advisoryAgainst(t *testing.T, s *Store, ghsa, severity, pkg, patched, withdrawn string) {
	t.Helper()
	if _, err := s.AddAdvisories([]Advisory{{
		GHSAID: ghsa, Severity: severity, Summary: ghsa + " summary",
		WithdrawnAt: withdrawn,
		Packages: []AdvisoryPackage{
			{Ecosystem: "actions", Name: pkg, VulnerableRange: "< 99", FirstPatched: patched},
		},
	}}); err != nil {
		t.Fatalf("AddAdvisories: %v", err)
	}
}

func remediationStore(t *testing.T) *Store {
	t.Helper()
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "actions/checkout", "v4", 10)
	seedDependency(t, s, "octo-org/app", "actions/cache", "v4", 12)
	return s
}

func find(t *testing.T, items []RemediationItem, ghsa string) RemediationItem {
	t.Helper()
	for _, it := range items {
		if it.GHSAID == ghsa {
			return it
		}
	}
	t.Fatalf("%s not ranked", ghsa)
	return RemediationItem{}
}

// The ranking the organization asked for is severity against how much it touches.
func TestRemediationRanksBySeverityTimesReach(t *testing.T) {
	s := remediationStore(t)
	advisoryAgainst(t, s, "GHSA-low-wide", "low", "actions/checkout", "2.0", "")
	advisoryAgainst(t, s, "GHSA-high-narrow", "high", "actions/cache", "2.0", "")

	// low severity across four repositories versus high severity in one.
	for _, repo := range []string{"octo-org/a", "octo-org/b", "octo-org/c", "octo-org/d"} {
		exposeRepo(t, s, repo, "GHSA-low-wide", "actions/checkout", 1)
	}
	exposeRepo(t, s, "octo-org/e", "GHSA-high-narrow", "actions/cache", 1)

	items, err := s.RemediationPriority(0, RepositoryScope{})
	if err != nil {
		t.Fatalf("RemediationPriority: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("ranked %d advisories, want 2", len(items))
	}

	// low(1) x 4 = 4 beats high(3) x 1 = 3.
	if items[0].GHSAID != "GHSA-low-wide" {
		t.Errorf("ranked %q first, want the wider one: %d vs %d",
			items[0].GHSAID, items[0].Priority, items[1].Priority)
	}
	if got := find(t, items, "GHSA-high-narrow").Priority; got != 3 {
		t.Errorf("high severity in one repository scored %d, want 3", got)
	}
}

// Direct and transitive both count as reach, but stay visible separately because they call for
// different work.
func TestRemediationSeparatesDirectFromTransitive(t *testing.T) {
	s := remediationStore(t)
	advisoryAgainst(t, s, "GHSA-mixed", "high", "actions/checkout", "2.0", "")
	exposeRepo(t, s, "octo-org/direct", "GHSA-mixed", "actions/checkout", 1)
	exposeRepo(t, s, "octo-org/via-a", "GHSA-mixed", "actions/checkout", 2)
	exposeRepo(t, s, "octo-org/via-b", "GHSA-mixed", "actions/checkout", 3)

	items, err := s.RemediationPriority(0, RepositoryScope{})
	if err != nil {
		t.Fatalf("RemediationPriority: %v", err)
	}
	it := find(t, items, "GHSA-mixed")

	if it.DirectRepos != 1 || it.TransitiveRepos != 2 {
		t.Errorf("direct/transitive = %d/%d, want 1/2", it.DirectRepos, it.TransitiveRepos)
	}
	if it.Priority != 3*3 {
		t.Errorf("Priority = %d, want severity times total reach", it.Priority)
	}
}

// An advisory with no published fix cannot be remediated by bumping a version, which a reader
// scheduling work has to know.
func TestRemediationFlagsUnfixableAdvisories(t *testing.T) {
	s := remediationStore(t)
	advisoryAgainst(t, s, "GHSA-nofix", "high", "actions/checkout", "", "")
	exposeRepo(t, s, "octo-org/a", "GHSA-nofix", "actions/checkout", 1)

	items, err := s.RemediationPriority(0, RepositoryScope{})
	if err != nil {
		t.Fatalf("RemediationPriority: %v", err)
	}
	if find(t, items, "GHSA-nofix").Fixable {
		t.Error("an advisory with no published patch reports as fixable")
	}
}

// Between advisories of equal weight, the one that can be acted on comes first.
func TestRemediationPrefersFixableOnTies(t *testing.T) {
	s := remediationStore(t)
	advisoryAgainst(t, s, "GHSA-aaa-nofix", "high", "actions/checkout", "", "")
	advisoryAgainst(t, s, "GHSA-zzz-fixed", "high", "actions/cache", "2.0", "")
	exposeRepo(t, s, "octo-org/a", "GHSA-aaa-nofix", "actions/checkout", 1)
	exposeRepo(t, s, "octo-org/a", "GHSA-zzz-fixed", "actions/cache", 1)

	items, err := s.RemediationPriority(0, RepositoryScope{})
	if err != nil {
		t.Fatalf("RemediationPriority: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("ranked %d advisories, want 2", len(items))
	}
	if items[0].GHSAID != "GHSA-zzz-fixed" {
		t.Errorf("ranked %q first, want the fixable one despite the later identifier",
			items[0].GHSAID)
	}
}

func TestRemediationExcludesWithdrawnAdvisories(t *testing.T) {
	s := remediationStore(t)
	advisoryAgainst(t, s, "GHSA-gone", "critical", "actions/checkout", "2.0",
		"2026-01-01T00:00:00Z")
	exposeRepo(t, s, "octo-org/a", "GHSA-gone", "actions/checkout", 1)

	items, err := s.RemediationPriority(0, RepositoryScope{})
	if err != nil {
		t.Fatalf("RemediationPriority: %v", err)
	}
	for _, it := range items {
		if it.GHSAID == "GHSA-gone" {
			t.Error("a withdrawn advisory was ranked for remediation")
		}
	}
}

// CVSS and EPSS describe the advisory, not its reach here, so they are reported but must not
// silently change the ranking the organization asked for.
func TestRemediationScoreIsSeverityTimesReachOnly(t *testing.T) {
	s := remediationStore(t)
	if _, err := s.AddAdvisories([]Advisory{{
		GHSAID: "GHSA-scored", Severity: "medium", Summary: "scored",
		CVSSv3Score: 9.8, EPSSPercentage: 0.9, HasEPSS: true,
		Packages: []AdvisoryPackage{
			{Ecosystem: "actions", Name: "actions/checkout", VulnerableRange: "< 9",
				FirstPatched: "9"},
		},
	}}); err != nil {
		t.Fatalf("AddAdvisories: %v", err)
	}
	exposeRepo(t, s, "octo-org/a", "GHSA-scored", "actions/checkout", 1)

	items, err := s.RemediationPriority(0, RepositoryScope{})
	if err != nil {
		t.Fatalf("RemediationPriority: %v", err)
	}
	it := find(t, items, "GHSA-scored")

	if it.Priority != 2 {
		t.Errorf("Priority = %d, want medium(2) x 1 repository", it.Priority)
	}
	if !it.HasCVSS || it.CVSSv3Score != 9.8 {
		t.Errorf("CVSS = %v (present=%v), want it reported alongside", it.CVSSv3Score, it.HasCVSS)
	}
	if !it.HasEPSS {
		t.Error("EPSS was not reported")
	}
}

func TestRemediationOfEmptyDatabase(t *testing.T) {
	s := advisoryStore(t)
	items, err := s.RemediationPriority(0, RepositoryScope{})
	if err != nil {
		t.Fatalf("RemediationPriority: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("ranked %d advisories with nothing exposed", len(items))
	}
}
