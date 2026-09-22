package view

import (
	"slices"
	"testing"
)

// The catalog is what a filter control is built from, and it has to describe every term the
// language can write.
//
// A browser that keeps its own list of terms has a second declaration, and two declarations drift:
// a term added here would never reach the control, and one removed here would stay in it offering
// a filter the server refuses by name. Reading the catalog off the same terms the serializer
// writes is what makes that impossible rather than merely unlikely.
func TestCatalogDescribesEveryTermTheLanguageWrites(t *testing.T) {
	described := map[string]Term{}
	for _, term := range Catalog() {
		described[term.Name] = term
	}

	for _, name := range everyTerm(t) {
		if _, ok := described[name]; !ok {
			t.Errorf("the catalog does not describe %q, so no control can offer it", name)
		}
	}
	if len(described) != len(Catalog()) {
		t.Error("the catalog names a term twice")
	}
}

// And nothing in it is a term the language does not have, or a control offers a filter that is
// refused the moment it is used.
func TestCatalogNamesOnlyRealTerms(t *testing.T) {
	for _, term := range Catalog() {
		if !IsTerm(term.Name) {
			t.Errorf("the catalog offers %q, which ABOM has no filter called", term.Name)
		}
	}
}

// An unlabeled term reaches the reader as a query-string key. `notProperties` is not a question
// anybody asked, and the help is what distinguishes terms whose names alone do not: `edges` from
// `edgeKinds`, `repositories` from `scope`.
func TestEveryCatalogTermCarriesItsOwnWords(t *testing.T) {
	for _, term := range Catalog() {
		if term.Label == "" {
			t.Errorf("%q has no label, so a control would show the parameter name", term.Name)
		}
		if term.Help == "" {
			t.Errorf("%q has no help, and its name alone does not say what it asks", term.Name)
		}
		if term.Label == term.Name {
			t.Errorf("%q is labeled with its own parameter name", term.Name)
		}
	}
}

// A term whose values are fixed carries them, so the control offers a closed list rather than a
// free-text box that produces a refusal on every typo.
func TestACatalogTermWithAFixedVocabularyCarriesIt(t *testing.T) {
	for _, term := range Catalog() {
		allowed, fixed := vocabulary[term.Name]
		if !fixed {
			continue
		}
		if !slices.Equal(term.Values, allowed) {
			t.Errorf("%q offers %v, but the language allows %v", term.Name, term.Values, allowed)
		}
	}
}

// A term no surface applies cannot be offered anywhere, which is the claim
// TestEveryTermIsAppliedBySomeConsumer makes from the other direction. Here it has to be visible
// to the control, so it can leave a term out rather than offering one the current surface refuses.
func TestEveryCatalogTermNamesTheSurfacesThatApplyIt(t *testing.T) {
	known := []string{
		SurfaceSet, SurfaceGraph, SurfaceInventory, SurfaceWorkflows,
		SurfaceJobs, SurfaceRepositories, SurfaceAdvisories, SurfaceRelationships,
		SurfaceDocument,
	}
	for _, term := range Catalog() {
		if len(term.Surfaces) == 0 {
			t.Errorf("%q names no surface, so no control could decide whether to offer it",
				term.Name)
		}
		for _, surface := range term.Surfaces {
			if !slices.Contains(known, surface) {
				t.Errorf("%q names surface %q, which nothing enforces", term.Name, surface)
			}
		}
	}
}

// The surfaces a term claims are the ones that really apply it. Stated separately from the term
// maps rather than derived by eye, because this is the half a control trusts when it decides not
// to offer something.
func TestCatalogSurfacesAgreeWithWhatEachSideEnforces(t *testing.T) {
	sides := map[string]map[string]bool{
		SurfaceSet:           setTerms,
		SurfaceGraph:         graphTerms,
		SurfaceInventory:     inventoryTerms,
		SurfaceWorkflows:     workflowTerms,
		SurfaceJobs:          jobTerms,
		SurfaceRepositories:  repositoryTerms,
		SurfaceAdvisories:    advisoryTerms,
		SurfaceRelationships: relationshipTerms,
	}

	for _, term := range Catalog() {
		for surface, enforced := range sides {
			claimed := slices.Contains(term.Surfaces, surface)
			// The graph hands some terms to whoever draws it, which is still applying them.
			applies := enforced[term.Name]
			if surface == SurfaceGraph {
				applies = AppliedByExplorer(term.Name)
			}
			if claimed != applies {
				t.Errorf("%q claims %s=%v, but that side applies it = %v",
					term.Name, surface, claimed, applies)
			}
		}
	}
}

// The document term set is the endpoint's refusal list, and an exported map would be writable by
// every package that imports it. A caller gets a copy it may sort, append to or discard, and the
// declaration this file exists to be the only copy of stays the only copy.
func TestTheDocumentTermsHandedOutAreACopyRatherThanTheDeclaration(t *testing.T) {
	names := DocumentTermNames()
	if len(names) == 0 {
		t.Fatal("no document terms, so this measures nothing")
	}

	names[0] = "scoop"
	if IsDocumentTerm("scoop") {
		t.Error("editing the returned slice reached the declaration")
	}
	if got := DocumentTermNames(); got[0] == "scoop" {
		t.Errorf("the next caller was handed the edit: %v", got)
	}
}

// Every surface the catalog says applies a term has to agree with the predicate that enforces it,
// and the document surface is the one whose membership is now read through a function.
func TestTheDocumentSurfaceOffersExactlyTheTermsItApplies(t *testing.T) {
	want := DocumentTermNames()

	var got []string
	for _, term := range Catalog() {
		if slices.Contains(term.Surfaces, SurfaceDocument) {
			got = append(got, term.Name)
		}
	}
	slices.Sort(got)

	if !slices.Equal(want, got) {
		t.Errorf("the document surface offers %v but applies %v", got, want)
	}
}
