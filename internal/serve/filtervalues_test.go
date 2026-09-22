package serve

import (
	"net/http/httptest"
	"strings"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// A term naming an endpoint that does not exist is worse than a term naming none: the control
// reads it as "these values are available", asks, and gets a 404 it has nothing to say about.
// The catalog is written in one package and the routes are registered in another, so nothing but
// this connects them.
func TestEveryValuesEndpointTheCatalogNamesIsServed(t *testing.T) {
	mux := (&Server{}).routes()

	for _, term := range viewspec.Catalog() {
		if term.ValuesFrom == "" {
			continue
		}
		t.Run(term.Name, func(t *testing.T) {
			path := "/" + strings.TrimPrefix(term.ValuesFrom, "/")
			request := httptest.NewRequest("GET", path, nil)
			// The catch-all under /api/ answers everything, so a served route has to be told from
			// the refusal by the pattern that claimed it rather than by a status.
			_, pattern := mux.Handler(request)
			if pattern == "" || pattern == "/api/" {
				t.Errorf("term %q reads its values from %q, which no route serves",
					term.Name, term.ValuesFrom)
			}
		})
	}
}

// A term with a closed vocabulary carries it, and one whose values come from the corpus does not
// carry a half-answer. Offering both would leave a control unable to tell a complete list from a
// sample of one.
func TestATermCarriesItsValuesOrNamesWhereTheyComeFromButNeverBoth(t *testing.T) {
	for _, term := range viewspec.Catalog() {
		if len(term.Values) > 0 && term.ValuesFrom != "" {
			t.Errorf("term %q carries %d values and also points at %q",
				term.Name, len(term.Values), term.ValuesFrom)
		}
	}
}
