package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

func deleteRequest(s *Server, id string, control bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest("DELETE", "/api/query-requests/"+id, nil)
	r.Host = "127.0.0.1:8080"
	r.SetPathValue("id", id)
	if control {
		r.Header.Set(controlHeader, "1")
	}
	w := httptest.NewRecorder()
	s.deleteQueryRequest(w, r)
	return w
}

func TestADeletedRequestLeavesTheListAndTheRestStay(t *testing.T) {
	s, _ := newServer(t)
	for _, title := range []string{"first", "second"} {
		w := httptest.NewRecorder()
		s.saveQueryRequest(w, httptest.NewRequest("POST", "/api/query-requests", bytes.NewBufferString(
			`{"title":"`+title+`","description":"x","compliant":"a","nonCompliant":"b","requestedBy":"operator"}`)))
		if w.Code != http.StatusOK {
			t.Fatalf("keeping %s answered %d: %s", title, w.Code, w.Body.String())
		}
	}
	first := readRequests(t, s).Requests[0].RowID

	w := deleteRequest(s, strconv.FormatInt(first, 10), true)
	if w.Code != http.StatusOK {
		t.Fatalf("deleting answered %d: %s", w.Code, w.Body.String())
	}
	var answered requestsAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatal(err)
	}
	if len(answered.Requests) != 1 || answered.Requests[0].RowID == first {
		t.Fatalf("the answer lists %+v, want only the request that was kept", answered.Requests)
	}
	if w := deleteRequest(s, strconv.FormatInt(first, 10), true); w.Code != http.StatusNotFound {
		t.Errorf("deleting it again answered %d, want 404", w.Code)
	}
}

func TestDeletingARequestIsAControlAct(t *testing.T) {
	s, _ := newServer(t)
	w := httptest.NewRecorder()
	s.saveQueryRequest(w, httptest.NewRequest("POST", "/api/query-requests", bytes.NewBufferString(aRequestBody)))
	if w := deleteRequest(s, "1", false); w.Code != http.StatusForbidden {
		t.Errorf("a delete without the control header answered %d, want 403", w.Code)
	}
	if got := readRequests(t, s).Requests; len(got) != 1 {
		t.Errorf("a refused delete removed the request: %+v", got)
	}
}

func TestARequestASessionIsWorkingCannotBeDeleted(t *testing.T) {
	s, st := newServer(t)
	w := httptest.NewRecorder()
	s.saveQueryRequest(w, httptest.NewRequest("POST", "/api/query-requests", bytes.NewBufferString(aRequestBody)))
	id := readRequests(t, s).Requests[0].RowID
	if _, err := st.StartAuthoringSession(id, "operator", "a-model"); err != nil {
		t.Fatal(err)
	}
	if w := deleteRequest(s, strconv.FormatInt(id, 10), true); w.Code != http.StatusConflict {
		t.Errorf("deleting a request under way answered %d, want 409: %s", w.Code, w.Body.String())
	}
}

func TestARequestAnAgentWorkedCannotBeDeleted(t *testing.T) {
	s, st := newServer(t)
	w := httptest.NewRecorder()
	s.saveQueryRequest(w, httptest.NewRequest("POST", "/api/query-requests", bytes.NewBufferString(aRequestBody)))
	id := readRequests(t, s).Requests[0].RowID
	session, err := st.StartAuthoringSession(id, "operator", "a-model")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EndAuthoringSession(session, store.SessionFinished, ""); err != nil {
		t.Fatal(err)
	}
	w = deleteRequest(s, strconv.FormatInt(id, 10), true)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "resubmit") {
		t.Errorf("deleting a worked request answered %d, want 409 naming what to do instead: %s", w.Code, w.Body.String())
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

// A query id the operator's pack holds asks to improve it, and one nothing holds asks to create
// it. One ABOM holds only from upstream is refused, because a session writes only inside the
// operator's pack, and an id no query could declare is refused because nothing could create it.
func TestARequestsQueryIDSaysWhetherToImproveOrCreate(t *testing.T) {
	s, st := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A"})
	if _, err := st.RecordQueryRun(store.QueryRun{
		AnalysisMode: store.AnalysisModeRegular, QueryID: "actions/unpinned-tag",
		QueryVersion: "u1", Pack: "codeql/actions-queries", Repository: "octo/app",
		DBCommit: "sha-one",
	}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		id, decision string
		code         int
		says         string
	}{
		{"acme/a", store.DecisionImprove, http.StatusOK, ""},
		{"acme/nothing", store.DecisionCreate, http.StatusOK, ""},
		{"", "", http.StatusOK, ""},
		{"actions/unpinned-tag", "", http.StatusBadRequest, "pack"},
		{"Not An Id", "", http.StatusBadRequest, "acme/"},
	} {
		w := requestAbout(t, s, c.id, "")
		if w.Code != c.code || !strings.Contains(w.Body.String(), c.says) {
			t.Errorf("a request about %q answered %d, want %d saying %q: %s", c.id, w.Code, c.code,
				c.says, w.Body.String())
			continue
		}
		if c.code != http.StatusOK {
			continue
		}
		if got := readRequests(t, s).Requests[0]; got.TargetDecision != c.decision {
			t.Errorf("a request about %q asks to %q, want %q", c.id, got.TargetDecision, c.decision)
		}
	}
	if n := len(readRequests(t, s).Requests); n != 3 {
		t.Errorf("kept %d requests, want the three a session could serve", n)
	}
}

// The catalog's metadata for a query it holds is read-only, so a request to improve it may not
// propose any, and the decision is the server's whatever the body says.
func TestARequestToImproveAQueryMayNotProposeMetadata(t *testing.T) {
	s, _ := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A"})
	body := `{"description":"d","compliant":"a","nonCompliant":"b","requestedBy":"operator",
	  "queryId":"%s","targetDecision":"create","metadata":{"precision":"high"}}`
	w := httptest.NewRecorder()
	s.saveQueryRequest(w, httptest.NewRequest("POST", "/api/query-requests",
		bytes.NewBufferString(fmt.Sprintf(body, "acme/a"))))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "acme/a") {
		t.Errorf("an improvement proposing metadata answered %d: %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.saveQueryRequest(w, httptest.NewRequest("POST", "/api/query-requests",
		bytes.NewBufferString(fmt.Sprintf(body, "acme/new-one"))))
	if w.Code != http.StatusOK {
		t.Fatalf("a new query with metadata answered %d: %s", w.Code, w.Body.String())
	}
	if got := readRequests(t, s).Requests[0]; got.Metadata.Precision != "high" ||
		got.TargetDecision != store.DecisionCreate {
		t.Errorf("kept %+v", got)
	}
}

// A form offers metadata from the same vocabularies the store checks against.
func TestTheRequestListCarriesTheMetadataVocabularies(t *testing.T) {
	s, _ := newServer(t)
	w := httptest.NewRecorder()
	s.queryRequests(w, httptest.NewRequest("GET", "/api/query-requests", nil))
	var got struct {
		Precisions []string `json:"precisions"`
		Severities []string `json:"severities"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Precisions, store.QueryRequestPrecisions()) ||
		!reflect.DeepEqual(got.Severities, store.QueryRequestSeverities()) {
		t.Errorf("the list offers %v and %v", got.Precisions, got.Severities)
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

// Every row says where it stands as a whole, and a request an agent is working on says so rather
// than reading its half-made steps as a verdict.
func TestEveryRequestCarriesOneOutcome(t *testing.T) {
	s, _ := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A"})
	for _, id := range []string{"acme/a", ""} {
		if w := requestAbout(t, s, id, ""); w.Code != http.StatusOK {
			t.Fatalf("answered %d: %s", w.Code, w.Body.String())
		}
	}
	requests, err := s.Store.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	var working int64
	for _, r := range requests {
		if r.QueryID == "acme/a" {
			working = r.RowID
		}
	}
	if _, err := s.Store.StartAuthoringSession(working, "operator", "claude-opus-5"); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	s.queryRequests(w, httptest.NewRequest("GET", "/api/query-requests", nil))
	var got struct {
		Requests []struct {
			store.QueryRequest
			Outcome authoring.Outcome `json:"outcome"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
	for _, r := range got.Requests {
		want := authoring.OutcomeUnstarted
		if r.RowID == working {
			want = authoring.OutcomeWorking
		}
		if r.Outcome.State != want || r.Outcome.Headline == "" {
			t.Errorf("request about %q has outcome %+v, want %s", r.QueryID, r.Outcome, want)
		}
	}
}

type stepsAnswer struct {
	Requests []struct {
		store.QueryRequest
		Steps   []authoring.Step  `json:"steps"`
		Outcome authoring.Outcome `json:"outcome"`
	} `json:"requests"`
}

func readSteps(t *testing.T, s *Server) map[int64][]authoring.Step {
	t.Helper()
	w := httptest.NewRecorder()
	s.queryRequests(w, httptest.NewRequest("GET", "/api/query-requests", nil))
	var got stepsAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
	out := map[int64][]authoring.Step{}
	for _, r := range got.Requests {
		out[r.RowID] = r.Steps
	}
	return out
}

func setStatus(t *testing.T, s *Server, id int64, status string) {
	t.Helper()
	body := `{"description":"flag a self-hosted runner","compliant":"a","nonCompliant":"b",
	  "queryId":"acme/a","status":"` + status + `"}`
	edit := httptest.NewRequest("PUT", "/api/query-requests/x", bytes.NewBufferString(body))
	edit.SetPathValue("id", strconv.FormatInt(id, 10))
	w := httptest.NewRecorder()
	s.editQueryRequest(w, edit)
	if w.Code != http.StatusOK {
		t.Fatalf("marking %d %s answered %d: %s", id, status, w.Code, w.Body.String())
	}
}

// Progress is read from the query a request names, which a later request may go on to change; a
// request that is done or declined keeps the steps it closed on, and reopening it reads them live.
func TestAClosedRequestKeepsTheProgressItClosedOn(t *testing.T) {
	s, _ := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A"})
	for range 2 {
		if w := requestAbout(t, s, "acme/a", ""); w.Code != http.StatusOK {
			t.Fatalf("answered %d: %s", w.Code, w.Body.String())
		}
	}
	before := readSteps(t, s)
	closed, open := int64(1), int64(2)
	if len(before[closed]) == 0 || len(before[open]) == 0 {
		t.Fatalf("want requests 1 and 2, read %+v", before)
	}
	for _, status := range []string{store.RequestDeclined, store.RequestDone} {
		setStatus(t, s, closed, status)
	}
	if err := os.Remove(filepath.Join(s.QueryDir, "test", "A", "A.expected")); err != nil {
		t.Fatal(err)
	}

	after := readSteps(t, s)
	if !reflect.DeepEqual(after[closed], before[closed]) {
		t.Errorf("the closed request moved with the pack:\n was %+v\n now %+v", before[closed], after[closed])
	}
	if reflect.DeepEqual(after[open], before[open]) {
		t.Fatalf("removing the expected file changed nothing, so this test proves nothing: %+v", after[open])
	}

	setStatus(t, s, closed, store.RequestOpen)
	if got := readSteps(t, s)[closed]; !reflect.DeepEqual(got, after[open]) {
		t.Errorf("a reopened request reads %+v, want the live %+v", got, after[open])
	}
}
