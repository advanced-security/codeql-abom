package view

import (
	"slices"
	"testing"
)

// The surfaces a named scope can be opened on, as the interface reads them.
var everySurface = []string{
	SurfaceSet, SurfaceGraph, SurfaceInventory, SurfaceWorkflows, SurfaceJobs,
	SurfaceRepositories, SurfaceAdvisories, SurfaceRelationships, SurfaceCoverage,
	SurfaceDocument, SurfaceRun,
}

// A page decides whether to offer a named scope from this answer, so a surface missing from it
// would read as one nobody asked about rather than one that applies the whole scope.
func TestEverySurfaceIsAnsweredForEvenWhenItAppliesTheWholeScope(t *testing.T) {
	refusals := Spec{}.Refusals()
	for _, surface := range everySurface {
		refused, ok := refusals[surface]
		if !ok {
			t.Errorf("%s is not answered for", surface)
			continue
		}
		if refused == nil || len(refused) != 0 {
			t.Errorf("%s refuses %#v of an empty scope, want an empty list", surface, refused)
		}
	}
	if len(refusals) != len(everySurface) {
		t.Errorf("answered for %d surfaces, want %d", len(refusals), len(everySurface))
	}
}

// What a page is told a surface refuses is what that surface's own endpoint refuses. A chip
// enabled by one declaration and refused by another is the refusal that arrives a click too late.
func TestEachSurfacesRefusalIsWhatThatSurfaceEnforces(t *testing.T) {
	spec := specCarryingEveryTerm()
	refusals := spec.Refusals()
	for surface, enforced := range map[string][]string{
		SurfaceSet:           spec.UnenforceableAsSet(),
		SurfaceGraph:         spec.UnenforceableAsGraph(),
		SurfaceInventory:     spec.UnenforceableAsInventory(),
		SurfaceWorkflows:     spec.UnenforceableAsWorkflows(),
		SurfaceJobs:          spec.UnenforceableAsJobs(),
		SurfaceRepositories:  spec.UnenforceableAsRepositories(),
		SurfaceAdvisories:    spec.UnenforceableAsAdvisories(),
		SurfaceRelationships: spec.UnenforceableAsRelationships(),
		SurfaceRun:           spec.UnenforceableAsRun(),
	} {
		if len(enforced) == 0 {
			t.Fatalf("%s applies every term, so this comparison proves nothing", surface)
		}
		if !slices.Equal(refusals[surface], enforced) {
			t.Errorf("%s is said to refuse %v and enforces %v", surface, refusals[surface], enforced)
		}
	}
}

// A run is built from a scope like any page, so a control building one offers only what a run
// applies — the same terms a run refuses the rest of by name.
func TestTheCatalogOffersARunExactlyTheTermsARunApplies(t *testing.T) {
	offered := 0
	for _, term := range Catalog() {
		claimed := slices.Contains(term.Surfaces, SurfaceRun)
		applies := repositoryTerms[term.Name] || setTerms[term.Name]
		if claimed != applies {
			t.Errorf("%q claims run=%v, but a run applies it = %v", term.Name, claimed, applies)
		}
		if claimed {
			offered++
		}
	}
	if offered == 0 {
		t.Error("a run is offered no term at all, so the comparison proves nothing")
	}
}

// The Coverage unit lists the crawl's queue, which its endpoint narrows by nothing in the language.
// Declaring that is what stops a scope it cannot apply being offered there as though it could.
func TestTheCoverageUnitAppliesNoFilter(t *testing.T) {
	for _, term := range Catalog() {
		if slices.Contains(term.Surfaces, SurfaceCoverage) {
			t.Errorf("%q is offered on the Coverage unit, which applies no filter", term.Name)
		}
	}
	spec, err := Parse("flags=findings&scope=owned")
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.Refusals()[SurfaceCoverage]; !slices.Equal(got, []string{"flags", "scope"}) {
		t.Errorf("the Coverage unit refuses %v, want every term the scope carries", got)
	}
}
