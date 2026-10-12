package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// appliesGrouping names endpoints that apply repository grouping, with required query context.
var appliesGrouping = map[string]string{
	"/api/remediation":    "",
	"/api/exposure":       "by=repository",
	"/api/repositories":   "",
	"/api/pinning":        "scope=owner",
	"/api/pinning/totals": "",
	"/api/nodes":          "",
	"/api/actions":        "",
	"/api/workflows":      "",
	"/api/jobs":           "",
	"/api/advisories":     "",
}

// Every endpoint refuses a parameter it does not apply, rather than answering a wider query.
func TestAnEndpointRefusesAParameterItDoesNotApply(t *testing.T) {
	srv, _ := unitServer(t)

	for _, path := range []string{
		"/api/remediation", "/api/advisories", "/api/exposure", "/api/nodes",
		"/api/pinning", "/api/pinning/totals", "/api/frontier", "/api/actions",
		"/api/workflows", "/api/jobs",
		"/api/repositories", "/api/properties", "/api/graph",
	} {
		_, applies := appliesGrouping[path]
		for _, param := range []string{"severity=error", "properties=business_id:3", "units=3"} {
			if applies && param == "properties=business_id:3" {
				continue
			}
			// The node listing answers the findings terms now: a node carries a finding of that
			// severity or it does not. Every other endpoint here still has nothing to apply it to.
			if path == "/api/actions" && param == "severity=error" {
				continue
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, localRequest(http.MethodGet, path+"?"+param, nil))

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
			srv.Handler().ServeHTTP(rec, localRequest(http.MethodGet, path+"?"+query, nil))
			if rec.Code != http.StatusOK {
				t.Errorf("%s?%s answered %d, want %d: %s",
					path, query, rec.Code, http.StatusOK, rec.Body.String())
			}
		}
	}
}

// The findings terms the node listing answers, accepted rather than refused.
//
// The refusal test above skips `/api/actions?severity=error` now, and a skip with nothing on the
// other side of it is how a term goes back to being silently ignored: it would drop out of the
// listing, stop being refused, and both tests would still pass.
func TestTheNodeListingAcceptsTheFindingsTermsItApplies(t *testing.T) {
	srv, _ := unitServer(t)

	for _, term := range []string{
		"severity=error", "rules=actions%2Funpinned-tag", "notRules=actions%2Funpinned-tag",
		"provider=codeql",
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, localRequest(http.MethodGet, "/api/actions?"+term, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("/api/actions?%s answered %d, want %d: %s",
				term, rec.Code, http.StatusOK, rec.Body.String())
		}
	}
}

// `repositories` on the node listing means the repository a finding sits in, which is the meaning
// the set path already gives it. The alternative reading — "dependencies used by these" — is a
// different question answered by `scope` and by the graph; one word for both is the divergence the
// enforcement split exists to prevent.
func TestTheNodeListingAppliesRepositoriesWithTheSetPathsMeaning(t *testing.T) {
	srv, _ := unitServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localRequest(
		http.MethodGet, "/api/actions?repositories=octo-org%2Fapp", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
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
		srv.Handler().ServeHTTP(rec, localRequest(http.MethodGet, path, nil))

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
	srv.Handler().ServeHTTP(rec, localRequest(http.MethodGet, "/api/frontier?top=lots", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("top=lots answered %d; a malformed number is not the default", rec.Code)
	}
}
