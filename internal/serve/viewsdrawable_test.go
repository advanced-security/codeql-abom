package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A view is saved wherever it was built and drawn wherever it is offered. One naming `boundary` is
// a perfectly good Explorer question and becomes a tab on Inventory that refuses itself the moment
// it is clicked — a correct refusal, arriving one click too late to be useful.
func TestTheCatalogSaysWhichTermsTheListingCannotDraw(t *testing.T) {
	srv, _ := unitServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/views", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/views answered %d: %s", rec.Code, rec.Body.String())
	}

	var views []NamedView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatalf("decoding the catalog: %v", err)
	}
	if len(views) == 0 {
		t.Fatal("the catalog is empty, so this proves nothing")
	}

	// Every built-in is a listing question, by construction: they are the tab strip.
	for _, v := range views {
		if v.Builtin && len(v.ListingRefuses) > 0 {
			t.Errorf("built-in %q is offered as a tab and names %v, which the listing refuses",
				v.Name, v.ListingRefuses)
		}
	}
}

// And a saved view that carries a graph-only term says so, rather than looking like any other tab.
func TestASavedGraphQuestionIsMarkedAsOneTheListingCannotDraw(t *testing.T) {
	srv, db := unitServer(t)
	if err := db.SaveView(store.SavedView{
		Name: "crossings", Predicate: "boundary=cross-owner",
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/views", nil))

	var views []NamedView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatalf("decoding the catalog: %v", err)
	}
	for _, v := range views {
		if v.Name != "crossings" {
			continue
		}
		if len(v.ListingRefuses) != 1 || v.ListingRefuses[0] != "boundary" {
			t.Fatalf("named %v, want the one term the listing cannot apply", v.ListingRefuses)
		}
		return
	}
	t.Fatal("the saved view is not in the catalog at all")
}
