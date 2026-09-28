package view

import (
	"strings"
	"testing"
)

// A term is enforced or refused, never accepted and dropped.
func TestTheSetPathNamesEveryTermItCannotApply(t *testing.T) {
	spec, err := Parse("rules=actions/*&scope=owned&q=checkout&flags=unanalyzed&kinds=action")
	if err != nil {
		t.Fatal(err)
	}

	got := spec.UnenforceableAsSet()
	want := []string{"flags", "kinds", "q", "scope"}
	if len(got) != len(want) {
		t.Fatalf("Unenforceable = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Unenforceable = %v, want %v", got, want)
		}
	}
}

// The terms it does apply are not reported as unenforceable, or every predicate is refused and the
// gate blocks nothing rather than something.
func TestAPredicateTheSetPathFullyAppliesNamesNothing(t *testing.T) {
	spec, err := Parse(
		"rules=actions/*&notRules=abom/noisy&provider=CodeQL&severity=error" +
			"&repositories=octo-org/app&properties=business_id:3&notProperties=business_id:0")
	if err != nil {
		t.Fatal(err)
	}

	if got := spec.UnenforceableAsSet(); len(got) != 0 {
		t.Errorf("Unenforceable = %v, want nothing; every one of these is applied", got)
	}
}

// The declaration is read rather than restated. A term removed from the language must stop being
// enforceable everywhere at once, which is the whole reason it lives here and not in each consumer.
func TestEveryEnforcedTermIsATermTheLanguageHas(t *testing.T) {
	for term := range setTerms {
		// A value shaped for the term, because some refuse a placeholder on their own terms and
		// that is a different failure from the language not knowing the name at all.
		_, err := Parse(term + "=" + "a:b")
		if err != nil && strings.Contains(err.Error(), "unknown filter") {
			t.Errorf("%q is declared enforceable and the language has no such term", term)
		}
	}
}

// An empty predicate asks nothing, so there is nothing to refuse. Reporting a term here would
// refuse the view that selects everything, which is a legitimate question.
func TestAnEmptyPredicateNamesNoUnenforceableTerm(t *testing.T) {
	if got := (Spec{}).UnenforceableAsSet(); len(got) != 0 {
		t.Errorf("Unenforceable = %v, want nothing", got)
	}
	if got := (Spec{}).UnenforceableAsGraph(); len(got) != 0 {
		t.Errorf("UnenforceableAsGraph = %v, want nothing", got)
	}
	if got := (Spec{}).ClientApplied(); len(got) != 0 {
		t.Errorf("ClientApplied = %v, want nothing", got)
	}
}

// The graph hands finding terms to the browser so it can keep relationships around matches.
func TestTheGraphNamesTheTermsItHandsToTheClient(t *testing.T) {
	spec, err := Parse("rules=actions/*&kinds=action&q=checkout&scope=owned&steps=true")
	if err != nil {
		t.Fatal(err)
	}

	got := spec.ClientApplied()
	want := []string{"kinds", "q", "rules"}
	if len(got) != len(want) {
		t.Fatalf("ClientApplied = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ClientApplied = %v, want %v", got, want)
		}
	}
	// `scope` and `steps` are applied by the server, so neither is handed anywhere.
	if len(spec.UnenforceableAsGraph()) != 0 {
		t.Errorf("UnenforceableAsGraph = %v, want nothing", spec.UnenforceableAsGraph())
	}
}

// `repositories` is applied neither by the graph server path nor by the browser.
func TestTheGraphRefusesATermNobodyApplies(t *testing.T) {
	spec, err := Parse("repositories=octo-org/app")
	if err != nil {
		t.Fatal(err)
	}

	got := spec.UnenforceableAsGraph()
	if len(got) != 1 || got[0] != "repositories" {
		t.Errorf("UnenforceableAsGraph = %v, want [repositories]", got)
	}
}

// Every term the language has belongs to exactly one side on the graph, or a term added later is
// silently dropped again by whichever list nobody remembered to update.
func TestEveryTermIsEitherAppliedByTheServerOrTheClient(t *testing.T) {
	for term := range setTerms {
		if graphTerms[term] && clientTerms[term] {
			t.Errorf("%q is claimed by both the server and the client", term)
		}
	}
	for term := range clientTerms {
		if graphTerms[term] {
			t.Errorf("%q is claimed by both the server and the client", term)
		}
	}
}
