package store

import "testing"

// A view selects; it never adjudicates.
//
// The decision to fail a pull request because a set is non-empty is the operator's, declared
// separately, exactly as a verdict must come from rule ids rather than from severity. A
// `blocking: true` field on a saved view would move the verdict into the selection, and the plan
// names it as the specific thing to refuse — because it will look like a convenience every time
// somebody wires the gate up.
//
// It is refused today by the rule that an unknown filter is an error rather than a no-op, which is
// the right mechanism. This guard exists so that relaxing that rule cannot quietly make a view
// able to carry a verdict.
func TestASavedViewCannotCarryAVerdict(t *testing.T) {
	s := newTestStore(t)

	for _, predicate := range []string{
		"blocking=true",
		"rules=actions%2F%2A&blocking=true",
		"scope=owned&fail=true",
		"rules=actions%2F%2A&severity=error&verdict=block",
	} {
		if err := s.SaveView(SavedView{Name: "policy", Predicate: predicate}); err == nil {
			t.Errorf("a view carrying %q was accepted; a view must not hold a verdict", predicate)
		}
	}
}

// The half that must keep working: a policy is a selection, and a selection is saveable.
func TestAGatePolicyIsAnOrdinarySavedView(t *testing.T) {
	s := newTestStore(t)

	if err := s.SaveView(SavedView{
		Name:      "block-on-injection",
		Predicate: "rules=actions/code-injection/*&severity=error",
		CreatedBy: DeclaredByInterface,
	}); err != nil {
		t.Fatalf("a gate policy expressed as a selection was refused: %v", err)
	}

	got, ok, err := s.SavedViewNamed("block-on-injection")
	if err != nil || !ok {
		t.Fatalf("SavedViewNamed: %v (found=%v)", err, ok)
	}
	if got.Predicate != "rules=actions%2Fcode-injection%2F%2A&severity=error" {
		t.Errorf("Predicate = %q, want the canonical spelling of the policy", got.Predicate)
	}
}
