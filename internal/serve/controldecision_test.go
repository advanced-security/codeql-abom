package serve

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func concrete(pattern string) (string, string) {
	method, path, _ := strings.Cut(pattern, " ")
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, "{") {
			parts[i] = "1"
		}
	}
	return method, strings.Join(parts, "/")
}

func headDecision(t *testing.T, s *store.Store, authzDir string) (authz.Decision, int64) {
	t.Helper()
	head, ok, err := s.Head()
	if err != nil || !ok {
		t.Fatalf("nothing logged: %v", err)
	}
	bundle, err := authz.ReadBundle(authzDir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := bundle.DecisionOf(head, s, nil)
	if err != nil {
		t.Fatalf("the head of the log is not a decision that verifies: %v", err)
	}
	return d, head.Seq
}

// Every mutation the API serves is an operation the guard registry holds, and the registry's route
// table names nothing that is not served. A route added without one fails here, before anyone can
// reach it.
func TestEveryAPIMutationIsARegisteredOperation(t *testing.T) {
	served := map[string]bool{}
	for _, pattern := range registeredAPIRoutes(t) {
		if !controlled(pattern) {
			continue
		}
		served[pattern] = true
		op, ok := controlOperations[pattern]
		if !ok {
			t.Errorf("%s is a mutation no operation names; add it to controlOperations", pattern)
			continue
		}
		if _, ok := authz.Lookup(op); !ok {
			t.Errorf("%s names %q, which the guard registry does not hold", pattern, op)
		}
	}
	for pattern := range controlOperations {
		if !served[pattern] {
			t.Errorf("controlOperations names %s, which is not a served mutation", pattern)
		}
	}
}

// Each control is decided, signed and logged before its handler runs, the decision naming its
// operation, its route and the digest of exactly what was asked.
func TestEveryAPIMutationSignsItsDecisionBeforeActing(t *testing.T) {
	s, st, dir := controlServer(t, "")
	routes := s.routes()
	body := `{"asked":true}`
	sum := sha256.Sum256([]byte(body))
	for _, pattern := range registeredAPIRoutes(t) {
		if !controlled(pattern) {
			continue
		}
		t.Run(pattern, func(t *testing.T) {
			var reached string
			guard := s.guardRequests(routes, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, _ := io.ReadAll(r.Body)
				reached = string(got)
			}))
			method, path := concrete(pattern)
			r := localRequest(method, path, strings.NewReader(body))
			r.Header.Set(controlHeader, "1")
			guard.ServeHTTP(httptest.NewRecorder(), r)

			if reached != body {
				t.Fatalf("the handler received %q; want the request as asked", reached)
			}
			d, _ := headDecision(t, st, dir)
			if d.Operation() != controlOperations[pattern] || !d.Allowed() || d.Subject() != "abom/route:"+pattern {
				t.Errorf("decided %s %s allowed=%v; want %s for this route", d.Operation(), d.Subject(), d.Allowed(), controlOperations[pattern])
			}
			if !strings.Contains(string(d.Evidence()), hex.EncodeToString(sum[:])) {
				t.Errorf("the evidence does not bind the request body: %s", d.Evidence())
			}
		})
	}
}

// A request the boundary refuses never reaches its handler and is not logged: a page in any
// browser can send one, and signing each would let it grow the append-only log without end.
func TestARequestTheBoundaryRefusesIsNotLogged(t *testing.T) {
	s, st, _ := controlServer(t, "")
	reached := false
	guard := s.guardRequests(s.routes(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	r := localRequest(http.MethodPost, "/api/discovery/start", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set(controlHeader, "1")
	w := httptest.NewRecorder()
	guard.ServeHTTP(w, r)

	if reached || w.Code != http.StatusForbidden {
		t.Errorf("a cross-site control answered %d and reached=%v", w.Code, reached)
	}
	if _, ok, _ := st.Head(); ok {
		t.Error("a boundary refusal was logged")
	}
}

// Unrecorded is undone: when the decision cannot be signed and logged, the control is not acted on.
func TestAControlWhoseDecisionCannotBeRecordedIsNotActedOn(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _, _ := controlServer(t, blocked)
	reached := false
	guard := s.guardRequests(s.routes(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	r := localRequest(http.MethodPost, "/api/discovery/start", nil)
	r.Header.Set(controlHeader, "1")
	w := httptest.NewRecorder()
	guard.ServeHTTP(w, r)

	if reached || w.Code != http.StatusServiceUnavailable {
		t.Errorf("an unrecorded control answered %d and reached=%v", w.Code, reached)
	}
}
