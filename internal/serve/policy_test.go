package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func postPolicy(t *testing.T, srv *Server, path, body string, out any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	req.Header.Set(controlHeader, "1")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if out != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decoding %s: %v", rec.Body.String(), err)
		}
	}
	return rec.Code
}

func getPolicyJSON(t *testing.T, srv *Server, path string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if out != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decoding %s: %v", rec.Body.String(), err)
		}
	}
	return rec.Code
}

// If a simulation and the gate can disagree there are two policy engines and one of them is wrong,
// and nobody finds out until a pull request is refused for a reason the simulation never showed.
// The set endpoint the gate asks and the simulation have to answer from the same evaluation.
func TestASimulationAndTheMatchEndpointAgreeOnTheSameInputs(t *testing.T) {
	srv, st := newServer(t)
	predicate := "rules=actions/unpinned-tag"
	if err := st.SavePolicy(store.Policy{
		Name: "no-unpinned", Predicate: predicate,
		ScopeKind: store.PolicyScopeOrganization, CreatedBy: "cli"}); err != nil {
		t.Fatal(err)
	}

	var matched Match
	if code := getPolicyJSON(t, srv, "/api/match?"+predicate, &matched); code != http.StatusOK {
		t.Fatalf("match returned %d", code)
	}
	var simulated store.PolicyOutcome
	if code := postPolicy(t, srv, "/api/policies/no-unpinned/simulate", "", &simulated); code != http.StatusOK {
		t.Fatalf("simulate returned %d", code)
	}

	if simulated.Nodes != matched.Total {
		t.Errorf("simulation saw %d nodes and the gate's endpoint saw %d",
			simulated.Nodes, matched.Total)
	}
	if simulated.Findings != matched.Findings {
		t.Errorf("simulation counted %d findings and the gate's endpoint counted %d",
			simulated.Findings, matched.Findings)
	}
}

// Adoption without a simulation is how a gate gets enabled on a backlog, blocks everything, and is
// switched off within a week. The refusal has to name what is missing.
func TestAdoptingAPolicyNobodySimulatedIsRefusedWithTheReason(t *testing.T) {
	srv, st := newServer(t)
	if err := st.SavePolicy(store.Policy{
		Name: "p", Predicate: "rules=actions/unpinned-tag",
		ScopeKind: store.PolicyScopeOrganization}); err != nil {
		t.Fatal(err)
	}

	var answer struct {
		Error string `json:"error"`
	}
	code := postPolicy(t, srv, "/api/policies/p/adopt", `{"by":"alice"}`, &answer)

	if code == http.StatusOK {
		t.Fatal("a policy was adopted with nothing known about what it would block")
	}
	if !strings.Contains(answer.Error, "simulat") {
		t.Errorf("refusal %q does not say what is missing", answer.Error)
	}
}

// A pattern cannot be audited and silently covers a repository created after it was written. The
// refusal names the reason, because a caller told only "400" will try a different spelling.
func TestAWaiverWrittenAsAPatternIsRefusedWithTheReason(t *testing.T) {
	srv, st := newServer(t)
	if err := st.SavePolicy(store.Policy{
		Name: "p", Predicate: "rules=actions/unpinned-tag",
		ScopeKind: store.PolicyScopeOrganization}); err != nil {
		t.Fatal(err)
	}

	var answer struct {
		Error string `json:"error"`
	}
	code := postPolicy(t, srv, "/api/policies/p/waivers",
		`{"kind":"exclusion","repository":"octo-org/*","reason":"convenience","grantedBy":"alice"}`,
		&answer)

	if code == http.StatusOK {
		t.Fatal("a pattern was accepted as a waiver")
	}
	if !strings.Contains(answer.Error, "pattern") {
		t.Errorf("refusal %q does not say why", answer.Error)
	}
}

// Every exclusion and exception in force, listed in full, on demand. This is not a report to be
// generated later; it is what makes the design defensible.
func TestTheAuditAnswerIsAvailableOnDemand(t *testing.T) {
	srv, st := newServer(t)
	if err := st.SavePolicy(store.Policy{
		Name: "p", Predicate: "rules=actions/unpinned-tag",
		ScopeKind: store.PolicyScopeOrganization}); err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	if code := postPolicy(t, srv, "/api/policies/p/waivers",
		`{"kind":"exception","repository":"octo-org/app","reason":"migration in flight",`+
			`"grantedBy":"alice","expiresAt":"`+expiry+`"}`, nil); code != http.StatusOK {
		t.Fatalf("granting the exception returned %d", code)
	}

	var audit []store.Waiver
	if code := getPolicyJSON(t, srv, "/api/policy-waivers", &audit); code != http.StatusOK {
		t.Fatalf("audit returned %d", code)
	}
	if len(audit) != 1 {
		t.Fatalf("audit listed %d waivers, want the one granted", len(audit))
	}
	w := audit[0]
	if w.Reason == "" || w.GrantedBy == "" || w.ExpiresAt.IsZero() || !w.InForce {
		t.Errorf("waiver %+v is missing what a reviewer needs", w)
	}
}

// Policies write the corpus and decide what gets refused, so they are held to the same control gate
// as everything else that changes state.
func TestWritingAPolicyIsControlGuarded(t *testing.T) {
	srv, _ := newServer(t)

	for _, path := range []string{
		"/api/policies/p/simulate", "/api/policies/p/adopt", "/api/policies/p/waivers",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		req.RemoteAddr = "203.0.113.7:9999"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s returned %d without the control header, want %d",
				path, rec.Code, http.StatusForbidden)
		}
	}
}
