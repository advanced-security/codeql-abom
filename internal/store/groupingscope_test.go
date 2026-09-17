package store

import (
	"path/filepath"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// groupingScopeFixture holds two owned repositories in different groupings, each exposed to the
// same advisory, so a report bounded to one grouping is distinguishable from the whole estate.
func groupingScopeFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo-org", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	ids := map[string]int64{}
	for repo, group := range map[string]string{"octo-org/app": "3", "octo-org/web": "7"} {
		id, err := s.UpsertRepository(repo, "")
		if err != nil {
			t.Fatalf("UpsertRepository(%s): %v", repo, err)
		}
		ids[repo] = id
		if err := s.ReplacePropertyValues(repo, []PropertyValue{
			{Name: "business_id", Value: group}}); err != nil {
			t.Fatalf("ReplacePropertyValues: %v", err)
		}
	}

	w := NewWriter(s, 0)
	if err := w.AddNode(Node{Key: "actions/checkout", Kind: "action", Owner: "actions",
		Repo: "checkout", IsExternal: true}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var nodeID int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM abom_node_live WHERE node_key = 'actions/checkout'`).Scan(&nodeID); err != nil {
		t.Fatalf("reading the node: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_advisory (ghsa_id, severity, summary, withdrawn_at, fetched_at)
		VALUES ('GHSA-test-0001', 'high', 'example', NULL, '2026-09-14T00:00:00Z')`); err != nil {
		t.Fatalf("inserting the advisory: %v", err)
	}
	for _, repo := range []string{"octo-org/app", "octo-org/web"} {
		if _, err := s.DB().Exec(`
			INSERT INTO abom_exposure (repository_row_id, node_row_id, ghsa_id, hops, path)
			VALUES (?, ?, 'GHSA-test-0001', 1, json_array('actions/checkout'))`,
			ids[repo], nodeID); err != nil {
			t.Fatalf("inserting the exposure for %s: %v", repo, err)
		}
	}
	return s
}

func groupThree() RepositoryScope {
	return RepositoryScope{Include: []viewspec.PropertyMatch{{Name: "business_id", Value: "3"}}}
}

// A repository listing bounded to a grouping shows that grouping's repositories and no others.
func TestARepositoryListingBoundedToAGroupingShowsOnlyIt(t *testing.T) {
	s := groupingScopeFixture(t)

	all, err := s.RepositoriesOverview("", 100, RepositoryScope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("unfiltered listed %d repositories, want 2", len(all))
	}

	one, err := s.RepositoriesOverview("", 100, groupThree())
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].FullName != "octo-org/app" {
		t.Errorf("business_id 3 listed %v, want only octo-org/app", one)
	}
}

// An exposure report bounded to a grouping reports that grouping's exposures.
func TestAnExposureReportBoundedToAGroupingShowsOnlyIts(t *testing.T) {
	s := groupingScopeFixture(t)

	all, err := s.ExposuresByRepository(100, RepositoryScope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("unfiltered reported %d exposures, want 2", len(all))
	}

	one, err := s.ExposuresByRepository(100, groupThree())
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Repository != "octo-org/app" {
		t.Errorf("business_id 3 reported %v, want only octo-org/app", one)
	}
}

// The same sharpness, on the other branch of the same endpoint. `/api/exposure` answers by
// repository or by advisory, and only the first was bounded — so a caller who omitted `by` was
// handed the whole organization under a predicate that said otherwise.
func TestExposureByAdvisoryCountsOnlyTheGroupingItWasBoundedTo(t *testing.T) {
	s := groupingScopeFixture(t)

	all, err := s.ExposureByAdvisory(RepositoryScope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Direct != 2 {
		t.Fatalf("unfiltered reported %+v, want one advisory reaching 2 repositories", all)
	}

	one, err := s.ExposureByAdvisory(groupThree())
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 {
		t.Fatalf("business_id 3 reported %d advisories, want 1", len(one))
	}
	if one[0].Direct != 1 {
		t.Errorf("business_id 3 counted %d directly affected repositories, want 1 — "+
			"the count spans the organization rather than the grouping", one[0].Direct)
	}
}

// The sharp one. A remediation row is an advisory, and its numbers count repositories, so the
// filter has to land before the aggregation. Applied after it, the row set would narrow while
// `DirectRepos` still counted the whole organization — an answer that looks bounded and is not.
func TestRemediationCountsOnlyTheGroupingItWasBoundedTo(t *testing.T) {
	s := groupingScopeFixture(t)

	all, err := s.RemediationPriority(100, RepositoryScope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].DirectRepos != 2 {
		t.Fatalf("unfiltered reported %+v, want one advisory reaching 2 repositories", all)
	}

	one, err := s.RemediationPriority(100, groupThree())
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 {
		t.Fatalf("business_id 3 reported %d rows, want the one advisory it is exposed to", len(one))
	}
	if one[0].DirectRepos != 1 {
		t.Errorf("DirectRepos = %d, want 1; the count still spans the organization",
			one[0].DirectRepos)
	}
}

// A grouping nothing is in reports nothing rather than everything, the way an empty selector does.
func TestAGroupingNobodyIsInReportsNothing(t *testing.T) {
	s := groupingScopeFixture(t)

	scope := RepositoryScope{
		Include: []viewspec.PropertyMatch{{Name: "business_id", Value: "404"}}}
	repos, err := s.RepositoriesOverview("", 100, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 0 {
		t.Errorf("a grouping nobody is in listed %d repositories", len(repos))
	}
}

// An empty answer is an empty list, never nil.
//
// A nil slice marshals to JSON `null`, which the interface's `.Select()` throws on rather than
// rendering as nothing. Latent until a filter existed — these reports always had rows on a
// populated corpus — and reachable the moment an answer can be bounded to a part of the estate
// that happens to be clean. Measured: `business_id:dev` returned `null` from `/api/exposure`.
func TestAnEmptyBoundedReportIsAnEmptyListNotNil(t *testing.T) {
	s := groupingScopeFixture(t)

	empty := RepositoryScope{
		Include: []viewspec.PropertyMatch{{Name: "business_id", Value: "404"}}}

	exposures, err := s.ExposuresByRepository(100, empty)
	if err != nil {
		t.Fatal(err)
	}
	if exposures == nil {
		t.Error("ExposuresByRepository returned nil, which serializes as null rather than []")
	}

	items, err := s.RemediationPriority(100, empty)
	if err != nil {
		t.Fatal(err)
	}
	if items == nil {
		t.Error("RemediationPriority returned nil, which serializes as null rather than []")
	}
}
