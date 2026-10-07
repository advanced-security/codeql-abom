package view

import (
	"strings"
	"testing"
)

// A built-in has to be written in the form the parser produces, or the same question asked by
// name and asked by hand would compare unequal — and the gate compares them.
func TestEveryBuiltinIsAlreadyCanonical(t *testing.T) {
	for _, b := range Builtins() {
		spec, err := Parse(b.Predicate)
		if err != nil {
			t.Errorf("built-in %q does not parse: %v", b.Name, err)
			continue
		}
		if got := spec.String(); got != b.Predicate {
			t.Errorf("built-in %q is spelled %q but canonicalizes to %q", b.Name, b.Predicate, got)
		}
	}
}

// Names are the text. Two built-ins sharing one would make which question you got depend on
// iteration order.
func TestBuiltinNamesAreDistinctAndPresent(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range Builtins() {
		if b.Name == "" || b.Title == "" {
			t.Errorf("a built-in is missing its name or title: %+v", b)
		}
		if seen[b.Name] {
			t.Errorf("two built-ins are named %q", b.Name)
		}
		seen[b.Name] = true
	}
	if !IsBuiltin("owned-findings") {
		t.Error("the arrival view is not recognized as a built-in")
	}
	if IsBuiltin("something-nobody-shipped") {
		t.Error("an unknown name was reported as a built-in")
	}
}

// "What you own" holds only what the declared repositories hold, and "Yours and what it runs" is a
// second question rather than its new name: retitled instead of narrowed, the owned tab kept
// listing 77 actions of which 25 were yours, and the strict question had no name at all.
func TestWhatYouOwnAsksOwnershipAndYoursAndWhatItRunsAsksTheSupplyChain(t *testing.T) {
	want := map[string]string{
		"What you own":                          "scope=owned",
		"What you own, with findings":           "flags=findings&scope=owned",
		"Yours and what it runs":                "scope=supply-chain",
		"Yours and what it runs, with findings": "flags=findings&scope=supply-chain",
	}
	got := map[string]string{}
	for _, b := range Builtins() {
		got[b.Title] = b.Predicate
	}
	for title, predicate := range want {
		if got[title] != predicate {
			t.Errorf("built-in %q asks %q, want %q", title, got[title], predicate)
		}
	}
	for _, b := range Builtins() {
		spec, err := Parse(b.Predicate)
		if err != nil {
			t.Fatalf("built-in %q does not parse: %v", b.Name, err)
		}
		runs := strings.Contains(b.Title, "what it runs")
		if spec.Scope == ScopeOwned && runs || spec.Scope == ScopeSupplyChain && !runs {
			t.Errorf("built-in %q asks scope=%s and is titled %q", b.Name, spec.Scope, b.Title)
		}
	}
}

// A tooltip that repeats the label says nothing, and one built-in without a description is a chip
// whose tooltip can only do that.
func TestEveryBuiltinSaysWhatItHolds(t *testing.T) {
	for _, b := range Builtins() {
		d := strings.TrimSpace(b.Description)
		if d == "" || strings.EqualFold(d, b.Title) || strings.HasPrefix(d, "Open ") {
			t.Errorf("built-in %q is described as %q, which says nothing its title does not", b.Name, b.Description)
		}
	}
}
