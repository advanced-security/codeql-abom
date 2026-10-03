package serve

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// A unit's route accepts every term the language declares the unit applies.
func TestAUnitsRouteAcceptsEveryTermItsUnitApplies(t *testing.T) {
	srv, _ := unitServer(t)
	routes := map[string]string{
		viewspec.SurfaceInventory:     "/api/actions",
		viewspec.SurfaceWorkflows:     "/api/workflows",
		viewspec.SurfaceJobs:          "/api/jobs",
		viewspec.SurfaceRepositories:  "/api/repositories",
		viewspec.SurfaceRelationships: "/api/relationships",
		viewspec.SurfaceAdvisories:    "/api/remediation",
		viewspec.SurfaceUses:          "/api/node/actions/checkout/-/uses",
	}
	for _, term := range viewspec.Catalog() {
		for _, surface := range term.Surfaces {
			path, ok := routes[surface]
			if !ok {
				continue
			}
			query := url.Values{term.Name: {sampleValue(term, surface)}}.Encode()
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+query, nil))
			if strings.Contains(rec.Body.String(), "Not applied here") {
				t.Errorf("%s?%s is refused by the route, but the %s unit applies %s: %s",
					path, query, surface, term.Name, rec.Body.String())
			}
		}
	}
}

// sampleValue is a value the term parses, so a refusal is about the term rather than its value.
func sampleValue(term viewspec.Term, surface string) string {
	switch {
	case len(term.SurfaceValues[surface]) > 0:
		return term.SurfaceValues[surface][0]
	case len(term.Values) > 0:
		return term.Values[0]
	case term.Arity == viewspec.ArityProperty:
		return "business_id:3"
	case term.Arity == viewspec.ArityNumber:
		return "1"
	case term.Arity == viewspec.ArityFlag:
		return "true"
	}
	return "octo-org/app"
}
