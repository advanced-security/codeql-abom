package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
