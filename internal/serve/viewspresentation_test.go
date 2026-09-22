package serve

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A saved view has to come back drawn the way it was saved, or naming a view is only half a
// bookmark: the question returns and the picture does not.
//
// Presentation travels beside the predicate rather than inside it. Folding `view` or `groupBy`
// into the filter language would make the server responsible for browser rendering and would pad
// every listing's refusal list with terms that are not filters.
func TestASavedViewIsServedWithHowItWasDrawn(t *testing.T) {
	s, db := newServer(t)
	if err := db.SaveView(store.SavedView{
		Name: "drawn", Predicate: "scope=owned", Presentation: "view=priority&groupBy=owner",
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	var views []NamedView
	body := get(t, s, "/api/views").Body.Bytes()
	if err := json.Unmarshal(body, &views); err != nil {
		t.Fatalf("decoding the catalog: %v", err)
	}

	for _, v := range views {
		if v.Name != "drawn" {
			continue
		}
		if v.Presentation != "view=priority&groupBy=owner" {
			t.Errorf("Presentation = %q, want it served verbatim", v.Presentation)
		}
		return
	}
	t.Fatal("the saved view was not in the catalog at all")
}

// A built-in is a question and nothing else. Shipping one with a drawing would decide how an
// operator's screen looks on their behalf, which is a different claim from "here is a question
// worth asking".
func TestABuiltinViewCarriesNoPresentation(t *testing.T) {
	s, _ := newServer(t)

	var views []NamedView
	if err := json.Unmarshal(get(t, s, "/api/views").Body.Bytes(), &views); err != nil {
		t.Fatalf("decoding the catalog: %v", err)
	}

	for _, v := range views {
		if v.Builtin && v.Presentation != "" {
			t.Errorf("built-in %q ships a presentation %q", v.Name, v.Presentation)
		}
	}
}

// Saving is where presentation enters, so the endpoint has to accept it. Dropping it here would
// make every save silently lose the drawing while reporting success.
func TestSavingAViewKeepsThePresentationItWasSentWith(t *testing.T) {
	s, db := newServer(t)

	// Loopback host and the control header: saving is a write, and the guard on writes checks
	// both. Without them this reads as "presentation was dropped" when it was never accepted.
	req := httptest.NewRequest("PUT", "http://127.0.0.1:8099/api/views/mine",
		strings.NewReader(`{"predicate":"scope=owned","presentation":"view=priority&hops=2"}`))
	req.SetPathValue("name", "mine")
	req.Header.Set(controlHeader, "1")
	rec := httptest.NewRecorder()
	s.saveView(rec, req)
	if rec.Code != 200 {
		t.Fatalf("saveView returned %d: %s", rec.Code, rec.Body.String())
	}

	saved, ok, err := db.SavedViewNamed("mine")
	if err != nil || !ok {
		t.Fatalf("SavedViewNamed: %v, found %v", err, ok)
	}
	if saved.Presentation != "view=priority&hops=2" {
		t.Errorf("Presentation = %q, want what was sent", saved.Presentation)
	}
}

// Naming a view resolves to its predicate. Presentation must not reach that path at all: a
// drawing that became a filter term would narrow the answer to match the picture.
func TestPresentationNeverReachesThePredicateAViewResolvesTo(t *testing.T) {
	s, db := newServer(t)
	if err := db.SaveView(store.SavedView{
		Name: "drawn", Predicate: "scope=owned", Presentation: "view=priority&groupBy=owner",
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	spec, named, err := s.resolveView(httptest.NewRequest("GET", "/api/graph/full?view=drawn", nil))
	if err != nil || !named {
		t.Fatalf("resolveView: %v, named %v", err, named)
	}
	if got := spec.String(); got != "scope=owned" {
		t.Errorf("predicate = %q, want the presentation kept out of it", got)
	}
}
