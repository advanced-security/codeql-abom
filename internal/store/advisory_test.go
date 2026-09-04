package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func advisoryStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func sampleAdvisory() Advisory {
	return Advisory{
		GHSAID: "GHSA-vqf5-2xx6-9wfm", CVEID: "CVE-2025-24362", Severity: "high",
		Summary: "CodeQL Action bundle debug artifacts", URL: "https://example.test/a",
		CVSSv3Score: 7.3, CVSSv3: "CVSS:3.1/AV:N",
		EPSSPercentage: 0.01245, EPSSPercentile: 0.66871, HasEPSS: true,
		CWEs: []string{"CWE-200"}, PublishedAt: "2025-02-01T00:00:00Z",
		Packages: []AdvisoryPackage{
			{Ecosystem: "actions", Name: "github/codeql-action",
				VulnerableRange: ">= 3.26.11, <= 3.28.2", FirstPatched: "3.28.3"},
			{Ecosystem: "actions", Name: "github/codeql-action",
				VulnerableRange: ">= 2.26.11, < 3.0.0"},
		},
	}
}

// seedDependency records an organisation repository using an action at a given reference.
func seedDependency(t *testing.T, s *Store, repo, nodeKey, ref string, line int) {
	t.Helper()
	id, err := s.UpsertRepository(repo, "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	w := NewWriter(s, 0)
	workflow := repo + "/.github/workflows/ci.yml"
	step := repo + "-step-" + ref
	owner, name := splitRepo(nodeKey)
	for _, n := range []Node{
		{Key: nodeKey, Kind: "action", IsExternal: true, Owner: owner, Repo: name},
		{Key: workflow, Kind: "workflow", RepositoryID: id},
		{Key: step, Kind: "step", RepositoryID: id, ContainerKey: workflow},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(Edge{
		Key: step + "-uses", SrcKey: step, DstKey: nodeKey, Kind: "step-uses-action",
		FilePath: ".github/workflows/ci.yml", StartLine: line, RefValue: ref,
		RepositoryID: id,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

// splitRepo takes owner/repo from a possibly subpath-qualified node key.
func splitRepo(nodeKey string) (owner, repo string) {
	parts := strings.Split(nodeKey, "/")
	if len(parts) < 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func TestAddAdvisoriesStoresFactsAndPackages(t *testing.T) {
	s := advisoryStore(t)

	n, err := s.AddAdvisories([]Advisory{sampleAdvisory()})
	if err != nil {
		t.Fatalf("AddAdvisories: %v", err)
	}
	if n != 1 {
		t.Errorf("wrote %d advisories, want 1", n)
	}

	var severity string
	var cvss, epss float64
	if err := s.DB().QueryRow(`
		SELECT severity, cvss_v3_score, epss_percentage FROM abom_advisory
		WHERE ghsa_id='GHSA-vqf5-2xx6-9wfm'`).Scan(&severity, &cvss, &epss); err != nil {
		t.Fatalf("reading advisory: %v", err)
	}
	if severity != "high" || cvss != 7.3 || epss != 0.01245 {
		t.Errorf("stored %q/%v/%v", severity, cvss, epss)
	}

	var packages int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_advisory_package`).Scan(&packages)
	if packages != 2 {
		t.Errorf("stored %d affected ranges, want 2", packages)
	}
}

// Severity, EPSS and patched versions all change after publication, so re-fetching has to
// refresh rather than duplicate.
func TestAddAdvisoriesRefreshesRatherThanDuplicates(t *testing.T) {
	s := advisoryStore(t)
	if _, err := s.AddAdvisories([]Advisory{sampleAdvisory()}); err != nil {
		t.Fatalf("first AddAdvisories: %v", err)
	}

	updated := sampleAdvisory()
	updated.Severity = "critical"
	updated.EPSSPercentage = 0.5
	updated.Packages[1].FirstPatched = "3.0.0"
	if _, err := s.AddAdvisories([]Advisory{updated}); err != nil {
		t.Fatalf("second AddAdvisories: %v", err)
	}

	var advisories, packages int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_advisory`).Scan(&advisories)
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_advisory_package`).Scan(&packages)
	if advisories != 1 || packages != 2 {
		t.Errorf("re-fetch produced %d advisories and %d packages, want 1 and 2",
			advisories, packages)
	}

	var severity, patched string
	s.DB().QueryRow(`SELECT severity FROM abom_advisory`).Scan(&severity)
	s.DB().QueryRow(`
		SELECT COALESCE(first_patched_version,'') FROM abom_advisory_package
		WHERE vulnerable_version_range = '>= 2.26.11, < 3.0.0'`).Scan(&patched)
	if severity != "critical" {
		t.Errorf("severity = %q, want the refreshed value", severity)
	}
	if patched != "3.0.0" {
		t.Errorf("first_patched_version = %q, want the refreshed value", patched)
	}
}

// Advisory packages name a repository, but the graph tracks subpath actions separately. An
// advisory against github/codeql-action has to reach github/codeql-action/init.
func TestAdvisoriesReachSubpathActions(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/init", "v3", 10)
	if _, err := s.AddAdvisories([]Advisory{sampleAdvisory()}); err != nil {
		t.Fatalf("AddAdvisories: %v", err)
	}

	reach, err := s.AdvisoriesByReach(0)
	if err != nil {
		t.Fatalf("AdvisoriesByReach: %v", err)
	}
	if len(reach) == 0 {
		t.Fatal("a subpath action was not matched to its repository's advisory")
	}
	if len(reach[0].Nodes) != 1 || reach[0].Nodes[0].NodeKey != "github/codeql-action/init" {
		t.Errorf("matched nodes = %+v", reach[0].Nodes)
	}
}

// One advisory covering several ranges and several subpath actions is one finding, not one per
// combination; multiplying it out buries the evidence.
func TestAdvisoryReachGroupsRangesAndNodes(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/init", "v3", 10)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/analyze", "v3", 12)
	if _, err := s.AddAdvisories([]Advisory{sampleAdvisory()}); err != nil {
		t.Fatalf("AddAdvisories: %v", err)
	}

	reach, err := s.AdvisoriesByReach(0)
	if err != nil {
		t.Fatalf("AdvisoriesByReach: %v", err)
	}

	if len(reach) != 1 {
		t.Fatalf("reported %d entries, want the advisory once", len(reach))
	}
	if len(reach[0].Ranges) != 2 {
		t.Errorf("ranges = %+v, want both", reach[0].Ranges)
	}
	if len(reach[0].Nodes) != 2 {
		t.Errorf("nodes = %+v, want both subpath actions", reach[0].Nodes)
	}
}

// An advisory published without a CVSS score must not read as one scored zero.
func TestAdvisoryReachDistinguishesUnscoredFromZero(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/init", "v3", 10)

	unscored := sampleAdvisory()
	unscored.CVSSv3Score = 0
	if _, err := s.AddAdvisories([]Advisory{unscored}); err != nil {
		t.Fatalf("AddAdvisories: %v", err)
	}

	reach, err := s.AdvisoriesByReach(0)
	if err != nil {
		t.Fatalf("AdvisoriesByReach: %v", err)
	}
	if len(reach) == 0 {
		t.Fatal("no reach reported")
	}
	if reach[0].HasCVSS {
		t.Error("an unscored advisory reports as having a CVSS score")
	}
	if !reach[0].HasEPSS {
		t.Error("a populated EPSS score was lost")
	}
}

// A retracted advisory must not be reported: its own publisher no longer stands behind it.
func TestWithdrawnAdvisoriesAreNotReported(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/init", "v3", 10)

	withdrawn := sampleAdvisory()
	withdrawn.WithdrawnAt = "2026-01-01T00:00:00Z"
	if _, err := s.AddAdvisories([]Advisory{withdrawn}); err != nil {
		t.Fatalf("AddAdvisories: %v", err)
	}

	reach, err := s.AdvisoriesByReach(0)
	if err != nil {
		t.Fatalf("AdvisoriesByReach: %v", err)
	}
	if len(reach) != 0 {
		t.Errorf("reported %d withdrawn advisories", len(reach))
	}
}

// The references actually used are the evidence a reader needs; reporting a range without them
// would leave no way to tell whether it applies.
func TestAdvisoryReachCarriesObservedReferences(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/init", "v3", 10)
	seedDependency(t, s, "octo-org/service", "github/codeql-action/init", "v2.26.11", 20)
	if _, err := s.AddAdvisories([]Advisory{sampleAdvisory()}); err != nil {
		t.Fatalf("AddAdvisories: %v", err)
	}

	reach, err := s.AdvisoriesByReach(0)
	if err != nil {
		t.Fatalf("AdvisoriesByReach: %v", err)
	}
	if len(reach) == 0 {
		t.Fatal("no reach reported")
	}

	joined := strings.Join(reach[0].Nodes[0].References, " ")
	for _, want := range []string{"v3", "v2.26.11"} {
		if !strings.Contains(joined, want) {
			t.Errorf("references %q missing %q", joined, want)
		}
	}
}

// The advisory API only knows repository-scoped names, so the query list has to collapse
// subpath actions onto their repository.
func TestAdvisoryPackagesAreRepositoryScoped(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/init", "v3", 10)
	seedDependency(t, s, "octo-org/app", "github/codeql-action/analyze", "v3", 12)
	seedDependency(t, s, "octo-org/app", "actions/checkout", "v4", 14)

	got, err := s.AdvisoryPackages()
	if err != nil {
		t.Fatalf("AdvisoryPackages: %v", err)
	}

	want := map[string]bool{"github/codeql-action": true, "actions/checkout": true}
	if len(got) != len(want) {
		t.Fatalf("packages = %v, want %d distinct repositories", got, len(want))
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("unexpected package %q", name)
		}
	}
}

func TestAddAdvisoriesRejectsMissingIdentifier(t *testing.T) {
	s := advisoryStore(t)
	if _, err := s.AddAdvisories([]Advisory{{Severity: "high"}}); err == nil {
		t.Error("an advisory without a GHSA id was accepted")
	}
}
