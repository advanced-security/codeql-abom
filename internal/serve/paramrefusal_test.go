package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// appliesGrouping names the endpoints that bound their answer to a repository grouping, against the
// rest of the query each one needs to answer at all.
//
// One declaration, read by the test that they accept the term and by the test that everything else
// refuses it. Stated twice, an endpoint gaining the term would be added to one list and not the
// other, and the pair would disagree about what the server does.
var appliesGrouping = map[string]string{
	"/api/remediation":    "",
	"/api/exposure":       "by=repository",
	"/api/repositories":   "",
	"/api/pinning":        "scope=owner",
	"/api/pinning/totals": "",
	"/api/nodes":          "",
	"/api/actions":        "",
	"/api/advisories":     "",
}

// Every endpoint refuses a parameter it does not apply.
//
// A reader who adds a term to a URL and gets a 200 believes they narrowed the answer. Measured on a
// crawled organization before this existed: `/api/repositories?severity=error` returned all 7
// matches, `/api/actions?properties=business_id:3` all 200, and `/api/frontier?notAParameter=1` all
// 5 — each of them the whole answer, wearing the name of a filtered one.
//
// The graph endpoints already refused an unknown term, because they parse the shared predicate.
// These eleven read their own parameters and so had nothing to refuse with.
func TestAnEndpointRefusesAParameterItDoesNotApply(t *testing.T) {
	srv, _ := unitServer(t)

	for _, path := range []string{
		"/api/remediation", "/api/advisories", "/api/exposure", "/api/nodes",
		"/api/pinning", "/api/pinning/totals", "/api/frontier", "/api/actions",
		"/api/repositories", "/api/properties", "/api/graph",
	} {
		_, applies := appliesGrouping[path]
		for _, param := range []string{"severity=error", "properties=business_id:3", "units=3"} {
			if applies && param == "properties=business_id:3" {
				continue
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+param, nil))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s?%s answered %d; it applies no such parameter",
					path, param, rec.Code)
				continue
			}
			if body := rec.Body.String(); !contains(body, "severity") &&
				!contains(body, "properties") && !contains(body, "units") {
				t.Errorf("%s?%s was refused without naming the parameter: %s", path, param, body)
			}
		}
	}
}

// Every endpoint that can be bounded to a repository grouping accepts the term rather than refusing
// it, which is the whole point of it reading it. Both directions of the filter, since an exclusion
// that is refused leaves the term half-enforced.
func TestTheReportsThatApplyAGroupingAcceptIt(t *testing.T) {
	srv, _ := unitServer(t)

	for path, extra := range appliesGrouping {
		for _, term := range []string{"properties=business_id%3A3", "notProperties=business_id%3A3"} {
			query := term
			if extra != "" {
				query = extra + "&" + term
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+query, nil))
			if rec.Code != http.StatusOK {
				t.Errorf("%s?%s answered %d, want %d: %s",
					path, query, rec.Code, http.StatusOK, rec.Body.String())
			}
		}
	}
}

// The parameters each endpoint does read still work, or the refusal has taken the endpoint with it.
func TestEveryEndpointStillAcceptsItsOwnParameters(t *testing.T) {
	srv, _ := unitServer(t)

	for _, path := range []string{
		"/api/remediation?top=5",
		"/api/advisories?top=5",
		"/api/exposure?by=repository&top=5",
		"/api/nodes?q=checkout&top=5",
		"/api/pinning?scope=owner&parent=&q=&style=&flagged=false&top=5",
		"/api/pinning/totals?q=&style=&flagged=false",
		"/api/frontier?status=done&top=5",
		"/api/actions?q=checkout&top=5",
		"/api/repositories?q=octo&top=5",
		"/api/properties?property=business_id",
		"/api/graph?focus=actions/checkout&hops=1&max=10&direction=dependencies&granularity=action&expand=false",
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code == http.StatusBadRequest {
			t.Errorf("%s was refused its own parameters: %s", path, rec.Body.String())
		}
	}
}

// A parameter with no value is a different question from an absent one, and the shared predicate
// already refuses it. An endpoint reading its own parameters must not be laxer than the language.
func TestAKnownParameterStillHasToParse(t *testing.T) {
	srv, _ := unitServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/frontier?top=lots", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("top=lots answered %d; a malformed number is not the default", rec.Code)
	}
}
