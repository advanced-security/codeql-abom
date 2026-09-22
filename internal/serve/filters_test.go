package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// The control that offers filters is built from this, so it answers without a corpus: a reader
// with nothing declared still has to be able to see what they could ask, and a filter list that
// waits on a crawl reads as an interface that is broken rather than empty.
func TestTheFilterCatalogAnswersWithoutAStore(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{}).filters(rec, httptest.NewRequest(http.MethodGet, "/api/filters", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []viewspec.Term
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the catalog: %v", err)
	}
	if len(got) != len(viewspec.Catalog()) {
		t.Errorf("served %d terms, the language writes %d", len(got), len(viewspec.Catalog()))
	}
}

// Every term arrives with the words and the shape a control needs, through the wire rather than
// only in the struct: a field that does not serialize is a field the browser does not have.
func TestTheServedCatalogCarriesWhatAControlNeeds(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{}).filters(rec, httptest.NewRequest(http.MethodGet, "/api/filters", nil))

	var got []viewspec.Term
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the catalog: %v", err)
	}
	for _, term := range got {
		if term.Name == "" || term.Label == "" || term.Help == "" || term.Arity == "" {
			t.Errorf("%+v reached the browser missing something a control needs", term)
		}
		if len(term.Surfaces) == 0 {
			t.Errorf("%q arrived naming no surface, so a control cannot place it", term.Name)
		}
	}
}

// A term with a closed list keeps it across the wire, or the control offers a free-text box for a
// value the server will refuse.
func TestAServedTermKeepsItsClosedList(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{}).filters(rec, httptest.NewRequest(http.MethodGet, "/api/filters", nil))

	var got []viewspec.Term
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the catalog: %v", err)
	}
	for _, term := range got {
		if term.Name != "scope" {
			continue
		}
		if len(term.Values) == 0 {
			t.Fatal("scope arrived with no values, though the language fixes them")
		}
		return
	}
	t.Fatal("the served catalog has no scope term")
}
