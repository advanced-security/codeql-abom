package serve

import (
	"net/http/httptest"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// A named view has to select exactly what its predicate selects, or naming a question changes it.
// The gate fails a pull request on a non-empty match, so the two spellings agreeing is the whole
// basis for letting a policy name a view instead of carrying a predicate.
func TestANamedViewAsksTheSameQuestionItsPredicateDoes(t *testing.T) {
	s := &Server{}
	for _, b := range viewspec.Builtins() {
		byName, err := s.graphPredicate(httptest.NewRequest("GET", "/api/graph/full?view="+b.Name, nil))
		if err != nil {
			t.Fatalf("resolving %q: %v", b.Name, err)
		}
		byHand, err := s.graphPredicate(httptest.NewRequest("GET", "/api/graph/full?"+b.Predicate, nil))
		if err != nil {
			t.Fatalf("parsing the predicate of %q: %v", b.Name, err)
		}
		if !byName.Equal(byHand) {
			t.Errorf("view %q resolves to %q but its predicate is %q",
				b.Name, byName.String(), byHand.String())
		}
	}
}

// An unknown name must fail loudly. A view that quietly matched everything would fail a pull
// request on a question nobody asked, which is the same defect as an ignored filter.
func TestAnUnknownViewNameIsRefusedRatherThanMatchingEverything(t *testing.T) {
	s, _ := newServer(t)
	_, _, err := s.resolveView(httptest.NewRequest("GET", "/api/graph/full?view=nothing-here", nil))
	if err == nil {
		t.Error("an unknown view name was accepted")
	}
}

// Two predicates in one request have no defined precedence, so combining them is refused rather
// than merged. Silently preferring one is how a reader ends up looking at a question they did not
// ask.
func TestANamedViewCannotBeCombinedWithAdHocFilters(t *testing.T) {
	s := &Server{}
	_, _, err := s.resolveView(
		httptest.NewRequest("GET", "/api/graph/full?view=owned&origins=seed", nil))
	if err == nil {
		t.Error("a named view combined with a filter was accepted")
	}
}

// A cursor is not a filter, so it must not make a named view look like a combination.
func TestACursorDoesNotCountAsCombiningFilters(t *testing.T) {
	s := &Server{}
	_, named, err := s.resolveView(
		httptest.NewRequest("GET", "/api/graph/delta?view=owned&since=12", nil), "since")
	if err != nil {
		t.Errorf("a named view with a cursor was refused: %v", err)
	}
	if !named {
		t.Error("the view name was not recognized alongside a cursor")
	}
}

// Asking nothing still means nothing. A request with no view must not resolve to one.
func TestARequestWithoutAViewNamesNone(t *testing.T) {
	s := &Server{}
	_, named, err := s.resolveView(httptest.NewRequest("GET", "/api/graph/full?scope=owned", nil))
	if err != nil {
		t.Fatalf("resolveView: %v", err)
	}
	if named {
		t.Error("a request with no view was treated as naming one")
	}
}
