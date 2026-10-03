package store

import (
	"slices"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// listedScope is propertyFixture with a third owned repository in unit 3 that nothing has ever
// fired in, and an analysis on record for all three.
func listedScope(t *testing.T) *Store {
	t.Helper()
	s := propertyFixture(t)
	if _, err := s.UpsertRepository("octo-org/quiet", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePropertyValues("octo-org/quiet", []PropertyValue{
		{Name: "business_id", Value: "3"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct{ repo, commit string }{
		{"octo-org/app", "sha-app"}, {"octo-org/tool", "sha-tool"}, {"octo-org/quiet", "sha-quiet"},
	} {
		if _, err := s.RecordQueryRun(QueryRun{
			AnalysisMode: AnalysisModeRegular, QueryID: "abom/edges",
			Repository: r.repo, DBCommit: r.commit, SourceFilter: "f0",
		}); err != nil {
			t.Fatalf("RecordQueryRun(%s): %v", r.repo, err)
		}
	}
	return s
}

// accountedFor names every repository a set resolved, to a database or to a gap, sorted.
func accountedFor(set DatabaseSet) []string {
	out := []string{}
	for _, d := range set.Databases {
		out = append(out, d.Repository)
	}
	for _, g := range set.Gaps {
		out = append(out, g.Repository)
	}
	slices.Sort(out)
	return out
}

// listedNames is what the repositories unit shows for a query, across every page.
func listedNames(t *testing.T, s *Store, q RepositoriesQuery) []string {
	t.Helper()
	page, err := s.RepositoriesPage(q)
	if err != nil {
		t.Fatalf("RepositoriesPage: %v", err)
	}
	out := []string{}
	for _, r := range page.Items {
		out = append(out, r.FullName)
	}
	slices.Sort(out)
	return out
}

// A run asked over a property covers every repository carrying it. Through findings, the set path
// reaches only the repositories something already fired in, which is nothing at all for a query
// nobody has run yet.
func TestARunOverAPropertyCoversEveryRepositoryTheUnitListsForIt(t *testing.T) {
	s := listedScope(t)
	unitThree := []viewspec.PropertyMatch{match("business_id", "3")}
	q := RepositoriesQuery{Grouping: RepositoryScope{Include: unitThree}}

	set, err := s.DatabasesListed(q, FindingSelector{Properties: unitThree})
	if err != nil {
		t.Fatalf("DatabasesListed: %v", err)
	}
	want := listedNames(t, s, q)
	if !slices.Equal(want, []string{"octo-org/app", "octo-org/quiet"}) {
		t.Fatalf("the unit lists %v; the fixture must list a clean repository beside one with findings",
			want)
	}
	if got := accountedFor(set); !slices.Equal(got, want) {
		t.Errorf("the run covers %v, want exactly the %v the repositories unit lists", got, want)
	}

	through, err := s.DatabasesMatching(FindingSelector{Properties: unitThree})
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	if slices.Contains(accountedFor(through), "octo-org/quiet") {
		t.Error("the set path reached the clean repository, so this measures no difference")
	}
}

// The owned scope holds upstream repositories too, and the unit leaves them out: they are not the
// organization's to analyze on its own behalf.
func TestARunOverTheOwnedScopeCoversTheRepositoriesTheUnitListsAndNoOthers(t *testing.T) {
	s := listedScope(t)
	q := RepositoriesQuery{Scope: ScopeOwned}

	set, err := s.DatabasesListed(q, FindingSelector{})
	if err != nil {
		t.Fatalf("DatabasesListed: %v", err)
	}
	if got, want := accountedFor(set), listedNames(t, s, q); !slices.Equal(got, want) {
		t.Errorf("the run covers %v, want exactly the %v the repositories unit lists", got, want)
	}
	if slices.Contains(accountedFor(set), "octo-org/shared") {
		t.Error("an upstream repository in the closure became a member of the run")
	}
}

// Every term narrows. A findings term keeps only the listed repositories it was found in: the rule
// fired in two repositories, and the scope lists one of them.
func TestAFindingsTermNarrowsAListedScopeToWhereTheFindingsSit(t *testing.T) {
	s := listedScope(t)

	set, err := s.DatabasesListed(RepositoriesQuery{Search: "tool"},
		FindingSelector{Rules: []string{"actions/unpinned-tag"}})
	if err != nil {
		t.Fatalf("DatabasesListed: %v", err)
	}
	if got := accountedFor(set); !slices.Equal(got, []string{"octo-org/tool"}) {
		t.Errorf("the run covers %v, want only octo-org/tool, listed and fired in", got)
	}
}

// Names narrow a listed scope too, in whatever case they were typed, and a name outside it is not
// a member: the scope is what was asked, and a name cannot widen it.
func TestANameNarrowsAListedScopeAndCannotWidenIt(t *testing.T) {
	s := listedScope(t)
	q := RepositoriesQuery{Scope: ScopeOwned, Grouping: pinningGroup("3").Scope}

	set, err := s.DatabasesListed(q, FindingSelector{
		Repositories: []string{"Octo-Org/Quiet", "octo-org/tool"},
	})
	if err != nil {
		t.Fatalf("DatabasesListed: %v", err)
	}
	if got := accountedFor(set); !slices.Equal(got, []string{"octo-org/quiet"}) {
		t.Errorf("the run covers %v, want only octo-org/quiet", got)
	}

	outside, err := s.DatabasesListed(q, FindingSelector{Repositories: []string{"octo-org/tool"}})
	if err != nil {
		t.Fatalf("DatabasesListed: %v", err)
	}
	if len(accountedFor(outside)) != 0 || outside.Unresolved == "" {
		t.Errorf("a name outside the scope resolved to %+v, want nothing and the reason", outside)
	}
}

// A scope the unit lists nothing for resolves to nothing, whatever else is asked. Handing an empty
// list to the set path would mean no narrowing at all, and a findings term would then reach the
// whole corpus under the name of a scope that holds none of it.
func TestAListedScopeHoldingNothingResolvesToNothingRatherThanEverything(t *testing.T) {
	s := listedScope(t)

	set, err := s.DatabasesListed(RepositoriesQuery{Scope: ScopeOwned, Search: "no-such-repository"},
		FindingSelector{Rules: []string{"actions/unpinned-tag"}})
	if err != nil {
		t.Fatalf("DatabasesListed: %v", err)
	}
	if got := accountedFor(set); len(got) != 0 {
		t.Errorf("an empty scope resolved to %v", got)
	}
	if set.Unresolved == "" {
		t.Error("an empty scope resolved to nothing without saying why")
	}
	if set.Databases == nil || set.Gaps == nil {
		t.Error("an empty answer marshals null lists, which the client throws on")
	}
}

// The shape travels with the scope, and a shape nothing was built in is a gap rather than the
// other shape's database.
func TestAListedScopeResolvesInTheShapeItWasAskedFor(t *testing.T) {
	s := listedScope(t)

	set, err := s.DatabasesListed(RepositoriesQuery{Search: "quiet"},
		FindingSelector{Mode: AnalysisModeExtended})
	if err != nil {
		t.Fatalf("DatabasesListed: %v", err)
	}
	if set.Mode != AnalysisModeExtended {
		t.Errorf("the answer reports mode %q, want extended", set.Mode)
	}
	if len(set.Databases) != 0 || !set.gapped("octo-org/quiet") {
		t.Errorf("an extended scope over a regular corpus resolved to %+v, want a gap", set)
	}
}
