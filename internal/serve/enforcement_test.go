package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The set endpoint and a policy are one evaluation, so they refuse the same predicate.
//
// Measured on a crawled organization before this existed: `/api/match` answered 400 for
// `kinds=action&steps=true&minReach=5&rules=actions/*` and naming the terms, while the identical
// text saved as a policy was stored verbatim and simulated to 632 nodes and 1,209 findings — what
// `rules=actions/*` alone selects. The policy enforced something wider than its own text said.
func TestAPolicyIsRefusedTheTermsTheSetEndpointRefuses(t *testing.T) {
	srv, _ := unitServer(t)

	predicate := "kinds=action&steps=true&minReach=5&rules=actions%2F*"

	match := httptest.NewRecorder()
	srv.Handler().ServeHTTP(match, httptest.NewRequest(http.MethodGet, "/api/match?"+predicate, nil))
	if match.Code != http.StatusBadRequest {
		t.Fatalf("/api/match answered %d; the fixture no longer exercises a refused predicate",
			match.Code)
	}

	save := httptest.NewRecorder()
	srv.Handler().ServeHTTP(save, policyRequest(predicate))
	if save.Code != http.StatusBadRequest {
		t.Fatalf("saving the same predicate as a policy answered %d, want %d: %s",
			save.Code, http.StatusBadRequest, save.Body.String())
	}
	for _, term := range []string{"kinds", "minReach", "steps"} {
		if !strings.Contains(save.Body.String(), term) {
			t.Errorf("the refusal does not name %q: %s", term, save.Body.String())
		}
	}
}

// The three terms neither refused nor applied were the sharper half: `/api/match` answered them
// with the unfiltered result and reported nothing, so a reader believed they had narrowed.
func TestTheSetEndpointRefusesTheTermsItSilentlyDropped(t *testing.T) {
	srv, _ := unitServer(t)

	for _, term := range []string{"scope=owned", "q=checkout", "flags=unanalyzed"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(
			http.MethodGet, "/api/match?rules=actions%2F*&"+term, nil))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("/api/match with %s answered %d; it applies no such term", term, rec.Code)
		}
	}
}

// A predicate the set path applies in full is still accepted, or the refusal has taken the
// endpoint with it and the gate blocks nothing rather than something.
func TestAFullyEnforceablePredicateIsStillAccepted(t *testing.T) {
	srv, _ := unitServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(
		http.MethodGet, "/api/match?rules=actions%2F*&severity=error&properties=business_id%3A3", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	save := httptest.NewRecorder()
	srv.Handler().ServeHTTP(save, policyRequest("rules=actions%2F*&severity=error"))
	if save.Code == http.StatusBadRequest {
		t.Errorf("an enforceable policy was refused: %s", save.Body.String())
	}
}

func policyRequest(predicate string) *http.Request {
	body := `{"name":"enforcement-probe","predicate":"` + predicate +
		`","scopeKind":"organization","scopeValue":""}`
	r := httptest.NewRequest(http.MethodPut, "/api/policies", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Abom-Control", "1")
	// Control endpoints answer only over loopback, and httptest addresses the request to example.com.
	r.Host = "127.0.0.1"
	return r
}
