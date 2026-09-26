package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/store"
)

type requestsAnswer struct {
	Requests []store.QueryRequest `json:"requests"`
	Kinds    []string             `json:"kinds"`
	Statuses []string             `json:"statuses"`
}

func readRequests(t *testing.T, s *Server) requestsAnswer {
	t.Helper()
	w := httptest.NewRecorder()
	s.queryRequests(w, httptest.NewRequest("GET", "/api/query-requests", nil))
	if w.Code != 200 {
		t.Fatalf("/api/query-requests answered %d: %s", w.Code, w.Body.String())
	}
	var got requestsAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the requests: %v", err)
	}
	return got
}

const aRequestBody = `{"description":"flag a self-hosted runner","compliant":"runs-on: ubuntu-latest",
  "nonCompliant":"runs-on: self-hosted","requestedBy":"operator"}`

func TestAQueryRequestIsKeptAndListedWithWhatItMaySay(t *testing.T) {
	s, _ := newServer(t)
	if got := readRequests(t, s); got.Requests == nil || len(got.Requests) != 0 {
		t.Fatalf("an empty list read as %v", got.Requests)
	}

	w := httptest.NewRecorder()
	s.saveQueryRequest(w, httptest.NewRequest("POST", "/api/query-requests",
		bytes.NewBufferString(aRequestBody)))
	if w.Code != http.StatusOK {
		t.Fatalf("keeping a request answered %d: %s", w.Code, w.Body.String())
	}

	got := readRequests(t, s)
	if len(got.Requests) != 1 || got.Requests[0].Status != store.RequestOpen {
		t.Errorf("listed %+v, want the one open request", got.Requests)
	}
	if len(got.Kinds) == 0 || len(got.Statuses) == 0 {
		t.Error("the list does not carry the vocabularies a form offers")
	}
}

func TestAQueryRequestWithoutBothExamplesIsRefused(t *testing.T) {
	s, _ := newServer(t)
	w := httptest.NewRecorder()
	s.saveQueryRequest(w, httptest.NewRequest("POST", "/api/query-requests",
		bytes.NewBufferString(`{"description":"x","compliant":"y","requestedBy":"operator"}`)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("a request with no non-compliant example answered %d, want 400", w.Code)
	}
}

func TestAQueryRequestIsEditedByItsNumber(t *testing.T) {
	s, _ := newServer(t)
	w := httptest.NewRecorder()
	s.saveQueryRequest(w, httptest.NewRequest("POST", "/api/query-requests",
		bytes.NewBufferString(aRequestBody)))
	id := readRequests(t, s).Requests[0].RowID

	edit := httptest.NewRequest("PUT", "/api/query-requests/1", bytes.NewBufferString(
		`{"description":"flag a self-hosted runner on pull_request","compliant":"a","nonCompliant":"b","status":"done"}`))
	edit.SetPathValue("id", "1")
	w = httptest.NewRecorder()
	s.editQueryRequest(w, edit)
	if w.Code != http.StatusOK {
		t.Fatalf("an edit answered %d: %s", w.Code, w.Body.String())
	}
	got := readRequests(t, s).Requests[0]
	if got.RowID != id || got.Status != store.RequestDone || got.RequestedBy != "operator" {
		t.Errorf("after the edit the request is %+v", got)
	}
}

func TestEditingARequestNobodyMadeIsNotFound(t *testing.T) {
	s, _ := newServer(t)
	edit := httptest.NewRequest("PUT", "/api/query-requests/9", bytes.NewBufferString(aRequestBody))
	edit.SetPathValue("id", "9")
	w := httptest.NewRecorder()
	s.editQueryRequest(w, edit)
	if w.Code != http.StatusNotFound {
		t.Errorf("editing request 9 answered %d, want 404", w.Code)
	}
}

func requestAbout(t *testing.T, s *Server, queryID, targetSource string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"description":"flag a self-hosted runner","compliant":"runs-on: ubuntu-latest",
	  "nonCompliant":"runs-on: self-hosted","requestedBy":"operator","queryId":"` + queryID +
		`","targetSource":"` + targetSource + `"}`
	w := httptest.NewRecorder()
	s.saveQueryRequest(w, httptest.NewRequest("POST", "/api/query-requests",
		bytes.NewBufferString(body)))
	return w
}

// A target is a selection, never text: a request naming a query nothing holds would read as a
// request about it, and nothing could ever act on it.
func TestARequestMayNameOnlyAQueryThatExists(t *testing.T) {
	s, st := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A"})
	if _, err := st.RecordQueryRun(store.QueryRun{
		AnalysisMode: store.AnalysisModeRegular, QueryID: "actions/unpinned-tag",
		QueryVersion: "u1", Pack: "codeql/actions-queries", Repository: "octo/app",
		DBCommit: "sha-one",
	}); err != nil {
		t.Fatal(err)
	}

	for id, want := range map[string]int{
		"acme/nothing": http.StatusBadRequest, "actions/unpinned-tag": http.StatusOK,
		"acme/a": http.StatusOK, "": http.StatusOK,
	} {
		if w := requestAbout(t, s, id, ""); w.Code != want {
			t.Errorf("a request about %q answered %d, want %d: %s", id, w.Code, want, w.Body.String())
		}
	}
	if n := len(readRequests(t, s).Requests); n != 3 {
		t.Errorf("kept %d requests, want the three about something that exists or nothing", n)
	}
}

// A request saved here is the operator's act, so they are who chose its target, whatever the
// body claims. The agent records its own decisions.
func TestTheOperatorIsRecordedAsChoosingTheTarget(t *testing.T) {
	s, _ := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A"})

	if w := requestAbout(t, s, "acme/a", store.TargetAgent); w.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", w.Code, w.Body.String())
	}
	if got := readRequests(t, s).Requests[0]; got.TargetSource != store.TargetOperator {
		t.Errorf("a target the operator picked is recorded as chosen by %q", got.TargetSource)
	}
	if w := requestAbout(t, s, "", store.TargetAgent); w.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", w.Code, w.Body.String())
	}
	if got := readRequests(t, s).Requests[0]; got.TargetSource != "" {
		t.Errorf("a request with no target is recorded as chosen by %q", got.TargetSource)
	}
}

// A request shows where its query stands without anybody reading QL: (a) first, then the
// target's working copy, or every step waiting when there is none.
func TestARequestCarriesItsTargetThenItsQuerysSteps(t *testing.T) {
	s, _ := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A"})
	for _, id := range []string{"acme/a", ""} {
		if w := requestAbout(t, s, id, ""); w.Code != http.StatusOK {
			t.Fatalf("answered %d: %s", w.Code, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	s.queryRequests(w, httptest.NewRequest("GET", "/api/query-requests", nil))
	var got struct {
		Requests []struct {
			store.QueryRequest
			Steps []authoring.Step `json:"steps"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
	for _, r := range got.Requests {
		names := []string{}
		for _, step := range r.Steps {
			names = append(names, step.Step)
		}
		if strings.Join(names, " ") != "target help expected database ast compiles tests" {
			t.Errorf("request about %q carries steps %v", r.QueryID, names)
			continue
		}
		switch r.QueryID {
		case "acme/a":
			if r.Steps[0].State != authoring.Passing || r.Steps[2].State != authoring.Passing {
				t.Errorf("a request about the working copy reads %+v", r.Steps)
			}
		case "":
			for _, step := range r.Steps {
				if step.State != authoring.Pending {
					t.Errorf("a request with no target reads %s at %s", step.State, step.Step)
				}
			}
		}
	}
}
