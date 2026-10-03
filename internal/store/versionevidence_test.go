package store

import (
	"maps"
	"slices"
	"testing"
)

// Who uses a dependency is narrowed by naming repositories, and the set is computed by the same
// restriction every bounded listing uses, so a details page and an inventory under one grouping
// count the same repositories. Nothing asked is no set at all, which is not an empty one.
func TestTheRepositoriesInScopeAreTheOnesABoundedListingCounts(t *testing.T) {
	s := pinningGroupingFixture(t)

	all, err := s.RepositoriesInScope(false, RepositoryScope{})
	if err != nil {
		t.Fatal(err)
	}
	if all != nil {
		t.Errorf("nothing was asked and %v came back; nil means unbounded", all)
	}
	three, err := s.RepositoriesInScope(false, pinningGroup("3").Scope)
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(three)); !slices.Equal(got, []string{"octo-org/tool"}) {
		t.Errorf("business_id:3 bounds to %v, want octo-org/tool", got)
	}
	if _, err := s.InsertUpstreamRepository("third/party"); err != nil {
		t.Fatal(err)
	}
	owned, err := s.RepositoriesInScope(true, RepositoryScope{})
	if err != nil {
		t.Fatal(err)
	}
	if !owned["octo-org/app"] || !owned["octo-org/tool"] || owned["third/party"] {
		t.Errorf("owned bounds to %v, want octo-org's repositories and nobody else's", owned)
	}
}
