package store

import "testing"

// The Repositories unit answers about repositories, so its columns are repository-shaped. Listing
// `kinds=repo` in the node grid was tried and refused: a `repo:` node carries no reach, references,
// findings or advisories, so every measured column drew absence as zero.
func TestRepositoriesPageReportsHowManyRowsItLeftOut(t *testing.T) {
	s := advisoryStore(t)
	for _, name := range []string{"octo-org/a", "octo-org/b", "octo-org/c"} {
		seedDependency(t, s, name, "actions/checkout", "v4", 1)
	}

	page, err := s.RepositoriesPage(RepositoriesQuery{Limit: 2})
	if err != nil {
		t.Fatalf("RepositoriesPage: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("page holds %d rows, want the 2 asked for", len(page.Items))
	}
	if page.Total != 3 {
		t.Errorf("total = %d, want the whole match rather than the page", page.Total)
	}
}

func TestRepositoriesPagingWalksTheMatchExactlyOnce(t *testing.T) {
	s := advisoryStore(t)
	for _, name := range []string{"octo-org/a", "octo-org/b", "octo-org/c", "octo-org/d"} {
		seedDependency(t, s, name, "actions/checkout", "v4", 1)
	}

	seen := map[string]int{}
	for offset := 0; ; offset += 2 {
		page, err := s.RepositoriesPage(RepositoriesQuery{Offset: offset, Limit: 2})
		if err != nil {
			t.Fatalf("RepositoriesPage: %v", err)
		}
		if len(page.Items) == 0 {
			break
		}
		for _, it := range page.Items {
			seen[it.FullName]++
		}
	}
	if len(seen) != 4 {
		t.Errorf("walked %d distinct repositories, want 4", len(seen))
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("repository %q appeared %d times", name, n)
		}
	}
}

// Sorting a page answers a narrower question than the column header asks, which is the defect the
// node grid already stopped having.
func TestRepositoriesSortTheWholeMatchRatherThanThePage(t *testing.T) {
	s := advisoryStore(t)
	for _, name := range []string{"octo-org/a", "octo-org/b", "octo-org/c"} {
		seedDependency(t, s, name, "actions/checkout", "v4", 1)
	}

	page, err := s.RepositoriesPage(RepositoriesQuery{Sort: "fullName", Desc: true, Limit: 1})
	if err != nil {
		t.Fatalf("RepositoriesPage: %v", err)
	}
	if len(page.Items) == 0 || page.Items[0].FullName != "octo-org/c" {
		t.Errorf("first row = %+v, want the last name across the whole match", page.Items)
	}
}

// A column name reaching SQL from a query string is an injection unless it is checked.
func TestRepositoriesRefuseASortColumnTheyDoNotHave(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/a", "actions/checkout", "v4", 1)

	if _, err := s.RepositoriesPage(RepositoriesQuery{Sort: "; DROP TABLE repository"}); err == nil {
		t.Error("an unknown sort column was accepted, want it refused by name")
	}
}

// Upstream repositories are not the organization's to remediate, and listing them beside the ones
// that are would imply otherwise.
func TestRepositoriesPageLeavesOutUpstream(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/a", "actions/checkout", "v4", 1)
	if _, err := s.InsertUpstreamRepository("someone/else"); err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}

	page, err := s.RepositoriesPage(RepositoriesQuery{})
	if err != nil {
		t.Fatalf("RepositoriesPage: %v", err)
	}
	for _, it := range page.Items {
		if it.FullName == "someone/else" {
			t.Error("an upstream repository is listed as one under review")
		}
	}
}
