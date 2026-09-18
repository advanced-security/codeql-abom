package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every refusal below is rendered verbatim on a security engineer's screen. `view:` is a Go
// package namespace, and a reader who meets it has been handed a stack frame in place of a
// sentence — the interface's own rule is that a refusal is a redirect, not a diagnostic.
//
// Asserted against the wire rather than against the source, because the prefix is added by
// `writeError` on one path and by the message itself on another.
func TestARefusalCarriesNoGoNamespace(t *testing.T) {
	srv, _ := unitServer(t)

	// One request per way a term can be refused: the route's own parameter list, the listing's
	// enforceability map, the graph's, and the shared predicate parser.
	for _, url := range []string{
		"/api/repositories?severity=error",
		"/api/actions?boundary=cross-owner",
		"/api/graph/full?sort=reach",
		"/api/actions?kindz=action",
		"/api/actions?top=lots",
		"/api/bom?boundary=cross-owner",
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s answered %d, so it refused nothing: %s", url, rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); strings.Contains(body, "view:") {
			t.Errorf("%s refused with a Go namespace on screen: %s", url, strings.TrimSpace(body))
		}
	}
}

// Naming the term is the floor. A reader who asked a reasonable question in the wrong place needs
// the right place named, and an API route is not a place a reader can go.
func TestTheListingRefusalNamesTheSurfaceThatAnswersIt(t *testing.T) {
	srv, _ := unitServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/actions?boundary=cross-owner", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "boundary") {
		t.Errorf("the refusal does not name the term it refused: %s", strings.TrimSpace(body))
	}
	if !strings.Contains(body, "Explorer") {
		t.Errorf("the refusal names no surface that answers it: %s", strings.TrimSpace(body))
	}
	if strings.Contains(body, "/api/") {
		t.Errorf("the refusal sends a reader to an API route: %s", strings.TrimSpace(body))
	}
}
