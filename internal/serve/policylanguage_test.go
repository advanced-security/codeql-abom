package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func putPolicy(t *testing.T, srv *Server, name, predicate string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name, "predicate": predicate})
	req := localRequest(http.MethodPut, "/api/policies", strings.NewReader(string(body)))
	req.Host = "127.0.0.1:8080"
	req.Header.Set(controlHeader, "1")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// advisoryServer holds one critical and one low advisory, each reaching octo-org/app directly.
func advisoryServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	srv, st := newServer(t)
	repo, err := st.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatal(err)
	}
	w := store.NewWriter(st, 0)
	for _, key := range []string{"actions/checkout", "actions/cache"} {
		owner, name, _ := strings.Cut(key, "/")
		if err := w.AddNode(store.Node{Key: key, Kind: "action", IsExternal: true, Owner: owner,
			Repo: name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	for ghsa, a := range map[string]struct{ severity, action string }{
		"GHSA-crit": {"critical", "actions/checkout"}, "GHSA-low": {"low", "actions/cache"},
	} {
		if _, err := st.AddAdvisories([]store.Advisory{{GHSAID: ghsa, Severity: a.severity,
			Packages: []store.AdvisoryPackage{{Ecosystem: "actions", Name: a.action,
				VulnerableRange: "< 9"}}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().Exec(`
			INSERT INTO abom_exposure (repository_row_id, node_row_id, ghsa_id, hops, path)
			SELECT ?, row_id, ?, 1, '[]' FROM abom_node WHERE node_key = ?`,
			repo, ghsa, a.action); err != nil {
			t.Fatal(err)
		}
	}
	return srv, st
}

// The endpoint a gate asks says what it counted, so a caller never reads "0 findings" as the
// answer to a question about advisories.
func TestTheMatchEndpointSaysWhetherItCountedFindingsOrAdvisories(t *testing.T) {
	srv, _ := advisoryServer(t)

	var advisories map[string]any
	if code := getPolicyJSON(t, srv, "/api/match?advisorySeverity=critical", &advisories); code != 200 {
		t.Fatalf("advisorySeverity was refused with %d", code)
	}
	if advisories["population"] != "advisories" || advisories["advisories"] != float64(1) ||
		advisories["total"] != float64(1) {
		t.Errorf("match = %v, want one critical advisory on one node", advisories)
	}

	var findings map[string]any
	if code := getPolicyJSON(t, srv, "/api/match?rules=abom%2Fone", &findings); code != 200 {
		t.Fatalf("rules was refused with %d", code)
	}
	if _, present := findings["advisories"]; present || findings["population"] != "findings" {
		t.Errorf("match = %v, want findings counted and no advisory count at all", findings)
	}
}

// Two questions in one predicate are refused by name, and so is a superset: only a policy can name
// another, and the set a gate asks for has one answer.
func TestTheMatchEndpointRefusesWhatHasNoSingleAnswer(t *testing.T) {
	srv, _ := advisoryServer(t)
	for query, want := range map[string]string{
		"advisorySeverity=high&rules=abom%2Fone": "policies=",
		"policies=a":                             "policies",
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, localRequest(http.MethodGet, "/api/match?"+query, nil))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s answered %d %s, want 400 naming %q", query, rec.Code, rec.Body.String(), want)
		}
	}
}

// A policy is refused where it is written for what its evaluation would refuse, so the refusal
// reaches whoever wrote it rather than the first pull request it would have blocked.
func TestSavingAPolicyRefusesWhatItsEvaluationWould(t *testing.T) {
	srv, _ := advisoryServer(t)
	for predicate, want := range map[string]string{
		"advisorySeverity=high&rules=abom%2Fone": "policies=",
		"policies=nobody":                        "nobody",
		"policies=a&properties=team:x":           "properties",
	} {
		if code, body := putPolicy(t, srv, "bad", predicate); code != http.StatusBadRequest ||
			!strings.Contains(body, want) {
			t.Errorf("saving %s answered %d %s, want 400 naming %q", predicate, code, body, want)
		}
	}
	if code, body := putPolicy(t, srv, "advisories", "advisorySeverity=critical"); code != 200 {
		t.Fatalf("an advisory policy was refused: %d %s", code, body)
	}
	if code, body := putPolicy(t, srv, "either", "policies=advisories"); code != 200 {
		t.Errorf("a superset naming a saved policy was refused: %d %s", code, body)
	}
}

// The advisories unit applies the terms a policy about advisories is written in, so the table and
// the policy answer the same question.
func TestTheAdvisoriesUnitNarrowsByPublishedSeverity(t *testing.T) {
	srv, _ := advisoryServer(t)
	var page store.AdvisoriesPage
	if code := getPolicyJSON(t, srv, "/api/remediation?advisorySeverity=critical", &page); code != 200 {
		t.Fatalf("remediation answered %d", code)
	}
	if len(page.Items) != 1 || page.Items[0].GHSAID != "GHSA-crit" {
		t.Errorf("items %+v, want only the critical advisory", page.Items)
	}
}

// A list refusing a term names that term directly.
func TestAListRefusingATermNamesTheFilter(t *testing.T) {
	srv, _ := advisoryServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localRequest(http.MethodGet,
		"/api/actions?advisorySeverity=high", nil))
	if rec.Code != http.StatusBadRequest || rec.Body.String() != "{\"error\":\"Not applied here: advisorySeverity.\"}\n" {
		t.Errorf("answered %d %s, want the concise refusal", rec.Code, rec.Body.String())
	}
}
