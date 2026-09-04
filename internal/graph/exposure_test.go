package graph

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// affects records an advisory against a package, as a fetch would.
func affects(t *testing.T, s *store.Store, ghsa, pkg, withdrawn string) {
	t.Helper()
	if _, err := s.AddAdvisories([]store.Advisory{{
		GHSAID: ghsa, Severity: "high", Summary: ghsa + " summary",
		WithdrawnAt: withdrawn,
		Packages: []store.AdvisoryPackage{
			{Ecosystem: "actions", Name: pkg, VulnerableRange: "< 99"},
		},
	}}); err != nil {
		t.Fatalf("AddAdvisories: %v", err)
	}
}

// exposureOfRepo reads back what was recorded for one repository.
func exposureOfRepo(t *testing.T, s *store.Store, repo, ghsa string) (hops int, path []string, ok bool) {
	t.Helper()
	var raw string
	err := s.DB().QueryRow(`
		SELECT e.hops, e.path
		FROM abom_exposure e
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE r.repository_full_name = ? AND e.ghsa_id = ?
		ORDER BY e.hops LIMIT 1`, repo, ghsa).Scan(&hops, &raw)
	if err != nil {
		return 0, nil, false
	}
	if err := json.Unmarshal([]byte(raw), &path); err != nil {
		t.Fatalf("decoding path: %v", err)
	}
	return hops, path, true
}

// The fixture has tool using actions/checkout directly, so the fix is available where the
// reference is.
func TestExposureRecordsDirectUse(t *testing.T) {
	s := fixture(t)
	affects(t, s, "GHSA-direct", "actions/checkout", "")

	if err := (&Builder{Store: s}).BuildExposure(); err != nil {
		t.Fatalf("BuildExposure: %v", err)
	}

	hops, path, ok := exposureOfRepo(t, s, "octo-org/tool", "GHSA-direct")
	if !ok {
		t.Fatal("a direct user was not recorded as exposed")
	}
	if hops != 1 {
		t.Errorf("hops = %d, want 1 for a direct use", hops)
	}
	if len(path) != 2 || path[len(path)-1] != "actions/checkout" {
		t.Errorf("path = %v, want the workflow then the affected action", path)
	}
}

// This is what a single-repository analysis cannot see: app never mentions actions/checkout,
// and only reaches it through a reusable workflow owned by another repository.
func TestExposureRecordsCrossRepositoryPath(t *testing.T) {
	s := fixture(t)
	affects(t, s, "GHSA-transitive", "actions/checkout", "")

	if err := (&Builder{Store: s}).BuildExposure(); err != nil {
		t.Fatalf("BuildExposure: %v", err)
	}

	hops, path, ok := exposureOfRepo(t, s, "octo-org/app", "GHSA-transitive")
	if !ok {
		t.Fatal("a transitively exposed repository was not recorded")
	}
	if hops != 2 {
		t.Errorf("hops = %d, want 2 through the shared workflow", hops)
	}

	joined := strings.Join(path, " -> ")
	for _, want := range []string{
		"octo-org/app/.github/workflows/ci.yml",
		"octo-org/shared/.github/workflows/build.yml",
		"actions/checkout",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("path %q missing %q", joined, want)
		}
	}
	if path[0] != "octo-org/app/.github/workflows/ci.yml" {
		t.Errorf("path starts at %q, want the exposed repository's own workflow", path[0])
	}
	if path[len(path)-1] != "actions/checkout" {
		t.Errorf("path ends at %q, want the affected action", path[len(path)-1])
	}
}

// A repository that reaches an action both directly and through an intermediary should hear
// about the direct route, because that is the one it can fix itself.
func TestExposureKeepsTheShortestRoute(t *testing.T) {
	s := fixture(t)

	// app already reaches checkout through shared; give it a direct reference too.
	id := repoID(t, s, "octo-org/app")
	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "actions/checkout", Kind: "action", IsExternal: true},
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: id},
		{Key: "app-direct-step", Kind: "step", RepositoryID: id,
			ContainerKey: "octo-org/app/.github/workflows/ci.yml"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(store.Edge{
		Key: "app-direct", SrcKey: "app-direct-step", DstKey: "actions/checkout",
		Kind: "step-uses-action", FilePath: ".github/workflows/ci.yml", StartLine: 60,
		RepositoryID: id,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	affects(t, s, "GHSA-shortest", "actions/checkout", "")
	if err := (&Builder{Store: s}).BuildExposure(); err != nil {
		t.Fatalf("BuildExposure: %v", err)
	}

	hops, _, ok := exposureOfRepo(t, s, "octo-org/app", "GHSA-shortest")
	if !ok {
		t.Fatal("no exposure recorded")
	}
	if hops != 1 {
		t.Errorf("hops = %d, want the direct route", hops)
	}
}

// A retracted advisory must not put repositories on an exposure list.
func TestExposureIgnoresWithdrawnAdvisories(t *testing.T) {
	s := fixture(t)
	affects(t, s, "GHSA-withdrawn", "actions/checkout", "2026-01-01T00:00:00Z")

	if err := (&Builder{Store: s}).BuildExposure(); err != nil {
		t.Fatalf("BuildExposure: %v", err)
	}

	var n int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_exposure`).Scan(&n)
	if n != 0 {
		t.Errorf("recorded %d exposures for a withdrawn advisory", n)
	}
}

// Only repositories under review are exposed; an upstream repository is not the organization's
// to remediate and would inflate the list.
func TestExposureExcludesUpstreamRepositories(t *testing.T) {
	s := fixture(t)
	upstreamUser(t, s, "third-party/tool", "actions/checkout")
	affects(t, s, "GHSA-upstream", "actions/checkout", "")

	if err := (&Builder{Store: s}).BuildExposure(); err != nil {
		t.Fatalf("BuildExposure: %v", err)
	}

	var n int
	s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_exposure e
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE r.repository_full_name = 'third-party/tool'`).Scan(&n)
	if n != 0 {
		t.Errorf("recorded %d exposures for an upstream repository", n)
	}
}

// Actions with no advisory against them must not appear at all.
func TestExposureCoversOnlyAffectedActions(t *testing.T) {
	s := fixture(t)
	affects(t, s, "GHSA-only-checkout", "actions/checkout", "")

	if err := (&Builder{Store: s}).BuildExposure(); err != nil {
		t.Fatalf("BuildExposure: %v", err)
	}

	var n int
	s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_exposure e
		JOIN abom_node n ON n.row_id = e.node_row_id
		WHERE n.node_key = 'actions/cache'`).Scan(&n)
	if n != 0 {
		t.Errorf("recorded %d exposures for an action with no advisory", n)
	}
}

func TestExposureIsIdempotent(t *testing.T) {
	s := fixture(t)
	affects(t, s, "GHSA-repeat", "actions/checkout", "")
	b := &Builder{Store: s}

	if err := b.BuildExposure(); err != nil {
		t.Fatalf("first BuildExposure: %v", err)
	}
	var first int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_exposure`).Scan(&first)

	if err := b.BuildExposure(); err != nil {
		t.Fatalf("second BuildExposure: %v", err)
	}
	var second int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_exposure`).Scan(&second)

	if first != second || first == 0 {
		t.Errorf("exposure count went %d -> %d", first, second)
	}
}

// Mutual references between repositories are legal, and a path walk must terminate on them.
func TestExposureTerminatesOnCycles(t *testing.T) {
	s := fixture(t)

	// Make shared depend on app's workflow, closing a loop with app -> shared.
	id := repoID(t, s, "octo-org/shared")
	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow",
			RepositoryID: repoID(t, s, "octo-org/app")},
		{Key: "octo-org/shared/.github/workflows/build.yml", Kind: "workflow", RepositoryID: id},
		{Key: "shared-loop-step", Kind: "step", RepositoryID: id,
			ContainerKey: "octo-org/shared/.github/workflows/build.yml"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(store.Edge{
		Key: "shared-loop", SrcKey: "shared-loop-step",
		DstKey: "octo-org/app/.github/workflows/ci.yml",
		Kind:   "job-uses-reusable-workflow", FilePath: ".github/workflows/build.yml",
		StartLine: 70, RepositoryID: id,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	affects(t, s, "GHSA-cycle", "actions/checkout", "")

	done := make(chan error, 1)
	go func() { done <- (&Builder{Store: s}).BuildExposure() }()
	if err := <-done; err != nil {
		t.Fatalf("BuildExposure: %v", err)
	}
}
