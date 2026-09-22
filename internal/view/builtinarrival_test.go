package view

import "testing"

// The 205-node picture the Explorer used to open on automatically is `scope=exposure` paired with
// `flags=findings`, and the catalog could not name it: `advisory-routes` carries the closure and
// nothing pairs it with findings.
//
// Now that arrival is chosen rather than assumed, a view a reader cannot pick is a view that does
// not exist. The closure alone draws more than the ceiling will render without being asked twice.
func TestTheAdvisoryRoutesWithFindingsViewCanBeNamed(t *testing.T) {
	b, ok := BuiltinNamed("exposure-findings")
	if !ok {
		t.Fatal("no built-in pairs the advisory routes with findings")
	}

	spec, err := Parse(b.Predicate)
	if err != nil {
		t.Fatalf("parsing %q: %v", b.Predicate, err)
	}
	if spec.Scope != "exposure" {
		t.Errorf("Scope = %q, want the advisory routes", spec.Scope)
	}
	if len(spec.Flags) != 1 || spec.Flags[0] != "findings" {
		t.Errorf("Flags = %v, want findings, which is the half that makes it a picture", spec.Flags)
	}
}
