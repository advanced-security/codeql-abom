package view

import "testing"

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
