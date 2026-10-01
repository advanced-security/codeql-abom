package view

import (
	"slices"
	"strings"
	"testing"
)

// A query's source is a finding's question as much as a query's: *what did our own rules find*
// against *what did the maintained set find*. It is its own word, because `origins` already means
// how a repository entered the corpus.
func TestSourceIsATermTheLanguageWrites(t *testing.T) {
	spec, err := Parse("source=upstream,custom")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !slices.Equal(spec.Sources, []string{SourceCustom, SourceUpstream}) {
		t.Errorf("parsed sources %v, want them sorted", spec.Sources)
	}
	again, err := Parse(spec.String())
	if err != nil || !again.Equal(spec) {
		t.Errorf("%q does not read back as itself: %+v %v", spec.String(), again, err)
	}
	if !IsTerm("source") {
		t.Error("source is not a term, so a refusal would call it a typo")
	}
}

// A source nobody can produce is refused by name, with the ones that exist, rather than matching
// nothing and reading as a clean answer.
func TestASourceABOMDoesNotKnowIsRefused(t *testing.T) {
	for _, text := range []string{"source=user", "source=upstream,github"} {
		_, err := Parse(text)
		if err == nil {
			t.Errorf("Parse(%q) was accepted", text)
			continue
		}
		for _, want := range []string{"source", SourceUpstream, SourceAbom, SourceCustom, SourceMixed} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusing %q says %q, which does not name %q", text, err, want)
			}
		}
	}
}

// The set path and the Inventory answer by what was found, so they apply it; every surface that
// cannot says so by name rather than answering with every finding.
func TestTheSurfacesThatAnswerASourceApplyItAndTheRestRefuseIt(t *testing.T) {
	spec := Spec{Sources: []string{SourceAbom}}
	if got := spec.UnenforceableAsSet(); len(got) != 0 {
		t.Errorf("the set path refuses %v", got)
	}
	if got := spec.UnenforceableAsInventory(); len(got) != 0 {
		t.Errorf("the Inventory refuses %v", got)
	}
	if got := spec.UnenforceableAsRun(); len(got) != 0 {
		t.Errorf("a run refuses %v, though it narrows by the set path's findings", got)
	}
	for name, refused := range map[string][]string{
		"graph":         spec.UnenforceableAsGraph(),
		"workflows":     spec.UnenforceableAsWorkflows(),
		"jobs":          spec.UnenforceableAsJobs(),
		"repositories":  spec.UnenforceableAsRepositories(),
		"advisories":    spec.UnenforceableAsAdvisories(),
		"relationships": spec.UnenforceableAsRelationships(),
	} {
		if !slices.Equal(refused, []string{"source"}) {
			t.Errorf("the %s surface refuses %v, want source named", name, refused)
		}
	}
}

func TestTheRepositoriesUnitAppliesTheFindingsFlag(t *testing.T) {
	spec, err := Parse("flags=findings&scope=owned")
	if err != nil {
		t.Fatal(err)
	}
	if refused := spec.UnenforceableAsRepositories(); len(refused) != 0 {
		t.Fatalf("Repositories refuses %v", refused)
	}
	other, err := Parse("flags=advisory")
	if err != nil {
		t.Fatal(err)
	}
	if refused := other.UnenforceableAsRepositories(); !slices.Equal(refused, []string{"flags=advisory"}) {
		t.Fatalf("Repositories refused %v, want the flag value it does not answer", refused)
	}
}

// A control offers the sources from the declaration that refuses the rest.
func TestTheCatalogOffersEverySource(t *testing.T) {
	for _, term := range Catalog() {
		if term.Name != "source" {
			continue
		}
		if term.Label != "Source" || term.Arity != ArityList {
			t.Errorf("source is offered as %q, %s", term.Label, term.Arity)
		}
		if want := []string{SourceUpstream, SourceAbom, SourceCustom, SourceMixed}; !slices.Equal(term.Values, want) {
			t.Errorf("source offers %v, want %v", term.Values, want)
		}
		if want := []string{SurfaceSet, SurfaceInventory, SurfaceRun, SurfacePolicy}; !slices.Equal(term.Surfaces, want) {
			t.Errorf("source is offered on %v, want %v", term.Surfaces, want)
		}
		return
	}
	t.Fatal("the catalog does not offer source")
}
