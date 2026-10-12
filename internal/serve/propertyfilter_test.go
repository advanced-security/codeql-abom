package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func unitServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	srv, st := newServer(t)
	declare(t, st, "octo-org")
	return srv, st
}

// The graph applies this one rather than refusing it, and the distinction is why: a finding
// predicate applied per node severs 74% to 99% of its own match's edges, because the graph is
// bipartite by kind. A property names *repositories*, so it narrows to a set of sources and keeps
// everything they reach — a subgraph rather than a scatter, by the mechanism `scope=owned` uses.
func TestTheGraphAppliesAPropertyRatherThanRefusingIt(t *testing.T) {
	srv, _ := unitServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localRequest(
		http.MethodGet, "/api/graph/full?properties=business_id:3", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// The delta answers the same question as the payload it updates, so it must accept the same terms
// — otherwise a filtered view stops updating the moment it is filtered.
func TestTheDeltaAcceptsWhatTheGraphAccepts(t *testing.T) {
	srv, _ := unitServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localRequest(
		http.MethodGet, "/api/graph/delta?since=0&properties=business_id:3", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// The set endpoint is where it is applied, so it must be accepted there.
func TestTheSetEndpointAcceptsAProperty(t *testing.T) {
	srv, _ := unitServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localRequest(
		http.MethodGet, "/api/match?properties=business_id:3", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// A term naming a value and no key would match against whatever the server happened to be
// configured with, which is the silent widening the parser exists to refuse.
func TestAPropertyTermWithNoKeyIsRefusedByTheEndpoint(t *testing.T) {
	srv, _ := unitServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localRequest(http.MethodGet, "/api/match?properties=3", nil))

	if rec.Code == http.StatusOK {
		t.Fatal("a property term carrying no key was answered rather than refused")
	}
}

// The document endpoint refuses filters it cannot apply.
func TestTheDocumentRefusesAParameterItDoesNotApply(t *testing.T) {
	srv, _ := unitServer(t)

	for _, query := range []string{"units=3", "severity=error", "notAParameter=1"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, localRequest(http.MethodGet, "/api/bom?"+query, nil))

		if rec.Code == http.StatusOK {
			t.Errorf("/api/bom?%s was answered; it applies no such filter", query)
			continue
		}
		if body := rec.Body.String(); !contains(body, "units") && !contains(body, "severity") &&
			!contains(body, "notAParameter") {
			t.Errorf("refusal of %q does not name the parameter: %s", query, body)
		}
	}
}

// The parameters it does apply still work, or the refusal has taken the endpoint with it.
func TestTheDocumentStillAcceptsItsOwnParameters(t *testing.T) {
	srv, _ := unitServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localRequest(
		http.MethodGet, "/api/bom?root=octo-org/app&format=native&download=true", nil))

	if rec.Code == http.StatusBadRequest {
		t.Errorf("its own parameters were refused: %s", rec.Body.String())
	}
}

// Reading the term through the shared parser is what makes it enforced rather than merely
// accepted, and a malformed one is the cheapest proof the parser ran at all.
func TestThePinningEndpointsRefuseAMalformedProperty(t *testing.T) {
	srv, _ := unitServer(t)

	for _, path := range []string{
		"/api/pinning?scope=owner&properties=3",
		"/api/pinning/totals?properties=3",
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, localRequest(http.MethodGet, path, nil))

		if rec.Code == http.StatusOK {
			t.Errorf("%s was answered; the term carries no key", path)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
