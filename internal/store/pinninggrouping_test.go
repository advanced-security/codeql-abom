package store

import (
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// pinningGroupingFixture puts the two seed repositories in different groupings under one owner.
//
// One owner over two groupings is the point: the owner row is an aggregate across both, so it can
// only report the bounded figure if the restriction ran before the GROUP BY.
func pinningGroupingFixture(t *testing.T) *Store {
	t.Helper()
	s := subgraphFixture(t)

	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo-org", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	// tool holds two pinnable references, app holds one.
	for repo, group := range map[string]string{"octo-org/tool": "3", "octo-org/app": "7"} {
		if err := s.ReplacePropertyValues(repo, []PropertyValue{
			{Name: "business_id", Value: group}}); err != nil {
			t.Fatalf("ReplacePropertyValues(%s): %v", repo, err)
		}
	}
	return s
}

func pinningGroup(value string) PinningFilter {
	return PinningFilter{Scope: RepositoryScope{
		Include: []viewspec.PropertyMatch{{Name: "business_id", Value: value}}}}
}

// The sharp one. Both repositories sit under `octo-org`, so an owner row bounded to one grouping
// reports the whole owner unless the restriction ran before the aggregation.
func TestPinningBoundedToAGroupingRecountsRatherThanDroppingRows(t *testing.T) {
	s := pinningGroupingFixture(t)

	owners, err := s.PinningLevel(PinningByOwner, "", PinningFilter{}, 0)
	if err != nil {
		t.Fatalf("PinningLevel: %v", err)
	}
	whole, _ := levelTotals(owners)
	if len(owners) != 1 || whole != 3 {
		t.Fatalf("unfiltered reported %d owners totalling %d pinnable, want 1 owner with 3",
			len(owners), whole)
	}

	three, err := s.PinningLevel(PinningByOwner, "", pinningGroup("3"), 0)
	if err != nil {
		t.Fatalf("PinningLevel(business_id:3): %v", err)
	}
	if len(three) != 1 {
		t.Fatalf("business_id:3 reported %d owner rows, want 1", len(three))
	}
	if three[0].Pinnable != 2 {
		t.Errorf("the owner row counts %d pinnable references under business_id:3, want 2 — "+
			"the count spans the owner rather than the grouping", three[0].Pinnable)
	}

	seven, err := s.PinningLevel(PinningByOwner, "", pinningGroup("7"), 0)
	if err != nil {
		t.Fatalf("PinningLevel(business_id:7): %v", err)
	}
	if len(seven) != 1 || seven[0].Pinnable != 1 {
		t.Fatalf("business_id:7 reported %+v, want one owner row with 1 pinnable", seven)
	}

	// The groupings partition the estate, so their parts account for the whole exactly.
	if three[0].Pinnable+seven[0].Pinnable != whole {
		t.Errorf("the parts total %d against a whole of %d",
			three[0].Pinnable+seven[0].Pinnable, whole)
	}
}

// Commit and floating still sum to pinnable once a grouping bounds them, which is what makes a
// bounded level readable as the same measurement rather than a different one.
func TestPinningMeasuresStillReconcileWhenBounded(t *testing.T) {
	s := pinningGroupingFixture(t)

	for _, scope := range []string{PinningByOwner, PinningByRepo, PinningByWorkflow} {
		rows, err := s.PinningLevel(scope, "", pinningGroup("3"), 0)
		if err != nil {
			t.Fatalf("PinningLevel(%s): %v", scope, err)
		}
		if len(rows) == 0 {
			t.Fatalf("%s reported nothing under business_id:3", scope)
		}
		for _, r := range rows {
			if r.Commit+r.Floating != r.Pinnable {
				t.Errorf("%s %s: commit %d + floating %d != pinnable %d",
					scope, r.ScopeKey, r.Commit, r.Floating, r.Pinnable)
			}
		}
	}
}

// The totals strip is the headline figure, and it reads from its own query rather than the level
// below it, so it has to be bounded in its own right.
func TestPinningTotalsBoundedToAGrouping(t *testing.T) {
	s := pinningGroupingFixture(t)

	whole, err := s.PinningTotals(PinningFilter{})
	if err != nil {
		t.Fatalf("PinningTotals: %v", err)
	}
	if whole.Pinnable != 3 {
		t.Fatalf("unfiltered totals report %d pinnable, want 3", whole.Pinnable)
	}

	three, err := s.PinningTotals(pinningGroup("3"))
	if err != nil {
		t.Fatalf("PinningTotals(business_id:3): %v", err)
	}
	if three.Pinnable != 2 {
		t.Errorf("business_id:3 totals %d pinnable, want 2", three.Pinnable)
	}
	// The owned split is derived from the owner level, so it has to carry the bound too rather
	// than reporting the organization beneath a bounded headline.
	if three.Yours.Yours != 2 {
		t.Errorf("business_id:3 attributes %d pinnable references to what you own, want 2",
			three.Yours.Yours)
	}
}

// A grouping nobody is in totals nothing, rather than falling through to the organization.
func TestPinningBoundedToAGroupingNobodyIsInReportsNothing(t *testing.T) {
	s := pinningGroupingFixture(t)

	rows, err := s.PinningLevel(PinningByOwner, "", pinningGroup("404"), 0)
	if err != nil {
		t.Fatalf("PinningLevel: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("a grouping nobody is in reported %d owner rows", len(rows))
	}

	total, err := s.PinningTotals(pinningGroup("404"))
	if err != nil {
		t.Fatalf("PinningTotals: %v", err)
	}
	if total.Pinnable != 0 {
		t.Errorf("a grouping nobody is in totals %d pinnable references", total.Pinnable)
	}
}
