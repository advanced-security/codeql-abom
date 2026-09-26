package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/harness"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// fakeAuthoring starts sessions on the store without an agent, and can be told to refuse.
type fakeAuthoring struct {
	store   *store.Store
	refuse  error
	started []int64
	stopped []int64
	flights []harness.Flight
	drafted [][]harness.Picked
}

func (f *fakeAuthoring) Draft(run int64, queryID string, picked []harness.Picked, by string) (store.AuthoringSession, error) {
	f.drafted = append(f.drafted, picked)
	if _, err := f.store.StartDraftSession(run, queryID, by, "claude-opus-5"); err != nil {
		return store.AuthoringSession{}, err
	}
	drafts, err := f.store.DraftSessions(run)
	if err != nil {
		return store.AuthoringSession{}, err
	}
	return drafts[0].AuthoringSession, nil
}

func (f *fakeAuthoring) StopDraft(int64) bool { return len(f.drafted) > 0 }

func (f *fakeAuthoring) Start(request int64, by string) (store.AuthoringSession, error) {
	if f.refuse != nil {
		return store.AuthoringSession{}, f.refuse
	}
	f.started = append(f.started, request)
	if _, err := f.store.StartAuthoringSession(request, by, "claude-opus-5"); err != nil {
		return store.AuthoringSession{}, err
	}
	all, err := f.store.AuthoringSessions(request)
	if err != nil {
		return store.AuthoringSession{}, err
	}
	return all[0], nil
}

func (f *fakeAuthoring) Stop(request int64) bool {
	for _, r := range f.started {
		if r == request {
			f.stopped = append(f.stopped, request)
			return true
		}
	}
	return false
}

func (f *fakeAuthoring) InFlight(request int64) []harness.Flight {
	for _, r := range f.started {
		if r == request {
			return f.flights
		}
	}
	return nil
}

// crawling is discovery in a given state.
type crawling struct {
	fakeDiscovery
	state string
}

func (c *crawling) Status() supervisor.Status { return supervisor.Status{State: c.state} }

func authoringServer(t *testing.T) (*Server, *store.Store, *fakeAuthoring, int64) {
	t.Helper()
	s, st := newServer(t)
	f := &fakeAuthoring{store: st}
	s.Authoring = f
	id, err := st.SaveQueryRequest(store.QueryRequest{Description: "d", Compliant: "c",
		NonCompliant: "n", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	return s, st, f, id
}

func sessionPost(t *testing.T, s *Server, path, body string, control bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	r.Host = "127.0.0.1:8080"
	if control {
		r.Header.Set(controlHeader, "1")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func sessionGet(t *testing.T, s *Server, path string, out any) int {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code == http.StatusOK && out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("decoding %s: %v\n%s", path, err, w.Body.String())
		}
	}
	return w.Code
}

// A session spends somebody's credits and writes into their pack, so a page on another origin
// must not be able to start one, exactly as it cannot start discovery.
func TestStartingASessionIsAGuardedControl(t *testing.T) {
	s, _, f, id := authoringServer(t)
	path := fmt.Sprintf("/api/query-requests/%d/sessions", id)
	if w := sessionPost(t, s, path, `{"by":"operator"}`, false); w.Code != http.StatusForbidden {
		t.Errorf("unguarded start answered %d, want 403", w.Code)
	}
	if len(f.started) != 0 {
		t.Fatal("an unguarded request started a session")
	}
	w := sessionPost(t, s, path, `{"by":"operator"}`, true)
	if w.Code != http.StatusOK {
		t.Fatalf("start answered %d: %s", w.Code, w.Body.String())
	}
	var got store.AuthoringSession
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.State != store.SessionRunning ||
		got.StartedBy != "operator" {
		t.Errorf("started %+v (%v)", got, err)
	}
}

// ql-mcp's CodeQL runs take none of ABOM's locks, and a crawl shares their cache: a session asked
// for while discovery works is refused, not left to stall the crawl for its whole length.
func TestASessionIsRefusedWhileDiscoveryIsOn(t *testing.T) {
	for _, state := range []string{supervisor.StateWorking, supervisor.StateIdle, supervisor.StatePausing} {
		s, _, f, id := authoringServer(t)
		s.Discovery = &crawling{state: state}
		w := sessionPost(t, s, fmt.Sprintf("/api/query-requests/%d/sessions", id), `{"by":"operator"}`, true)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "discovery") {
			t.Errorf("discovery %s: answered %d %s, want 409 naming discovery", state, w.Code, w.Body.String())
		}
		if len(f.started) != 0 {
			t.Errorf("discovery %s: a session started", state)
		}
	}
	for _, state := range []string{supervisor.StatePaused, supervisor.StateStopped} {
		s, _, _, id := authoringServer(t)
		s.Discovery = &crawling{state: state}
		if w := sessionPost(t, s, fmt.Sprintf("/api/query-requests/%d/sessions", id), `{"by":"operator"}`,
			true); w.Code != http.StatusOK {
			t.Errorf("discovery %s: answered %d %s", state, w.Code, w.Body.String())
		}
	}
}

func TestARefusedSessionSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		refuse error
		want   int
	}{
		{harness.ErrNoAgent, http.StatusBadRequest},
		{harness.ErrBusy, http.StatusConflict},
		{fmt.Errorf("request 1: %w", store.ErrSessionUnderWay), http.StatusConflict},
		{fmt.Errorf("request 9: %w", store.ErrNoQueryRequest), http.StatusNotFound},
		{errors.New("actions/unpinned-tag is not a query in the pack"), http.StatusBadRequest},
	} {
		s, _, f, id := authoringServer(t)
		f.refuse = tc.refuse
		w := sessionPost(t, s, fmt.Sprintf("/api/query-requests/%d/sessions", id), `{"by":"operator"}`, true)
		if w.Code != tc.want || !strings.Contains(w.Body.String(), strings.SplitN(tc.refuse.Error(), ";", 2)[0]) {
			t.Errorf("%v: answered %d %s, want %d with the reason", tc.refuse, w.Code, w.Body.String(), tc.want)
		}
	}
	s, _ := newServer(t)
	w := sessionPost(t, s, "/api/query-requests/1/sessions", `{"by":"operator"}`, true)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "query pack") {
		t.Errorf("a server with no authoring answered %d %s", w.Code, w.Body.String())
	}
}

type sessionsAnswer struct {
	Sessions []store.AuthoringSession `json:"sessions"`
	InFlight []harness.Flight         `json:"inFlight"`
}

// What CodeQL is doing on the agent's behalf right now is the one thing the store cannot say.
func TestARequestsSessionsAreListedWithTheCodeQLUnderWay(t *testing.T) {
	s, _, f, id := authoringServer(t)
	path := fmt.Sprintf("/api/query-requests/%d/sessions", id)
	var empty sessionsAnswer
	if code := sessionGet(t, s, path, &empty); code != http.StatusOK || empty.Sessions == nil ||
		len(empty.Sessions) != 0 || empty.InFlight == nil {
		t.Fatalf("no sessions answered %d %+v", code, empty)
	}
	f.flights = []harness.Flight{{Tool: "ql-mcp-codeql_test_run", Subject: "test/A", Step: "tests",
		Since: time.Now()}}
	if w := sessionPost(t, s, path, `{"by":"operator"}`, true); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var got sessionsAnswer
	sessionGet(t, s, path, &got)
	if len(got.Sessions) != 1 || len(got.InFlight) != 1 || got.InFlight[0].Subject != "test/A" {
		t.Errorf("listed %+v", got)
	}
}

func TestStoppingASessionNobodyIsRunningIsAConflict(t *testing.T) {
	s, _, f, id := authoringServer(t)
	stop := fmt.Sprintf("/api/query-requests/%d/sessions/stop", id)
	if w := sessionPost(t, s, stop, "", true); w.Code != http.StatusConflict {
		t.Errorf("stopping nothing answered %d", w.Code)
	}
	sessionPost(t, s, fmt.Sprintf("/api/query-requests/%d/sessions", id), `{"by":"operator"}`, true)
	if w := sessionPost(t, s, stop, "", false); w.Code != http.StatusForbidden {
		t.Errorf("unguarded stop answered %d", w.Code)
	}
	if w := sessionPost(t, s, stop, "", true); w.Code != http.StatusOK || len(f.stopped) != 1 {
		t.Errorf("stop answered %d %s", w.Code, w.Body.String())
	}
}

type eventsAnswer struct {
	Events []store.AuthoringEvent `json:"events"`
	Next   int64                  `json:"next"`
}

// A reader following a live session asks only for what came after the last event it holds.
func TestATimelineIsReadOnFromWhereTheReaderLeftOff(t *testing.T) {
	s, st, _, id := authoringServer(t)
	session, err := st.StartAuthoringSession(id, "operator", "claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, tool := range []string{"bash", "view", "ql-mcp-codeql_test_run"} {
		rowID, err := st.RecordAuthoringEvent(store.AuthoringEvent{SessionID: session, At: "2026-09-23T12:00:00Z",
			Hook: "pre-tool-use", Tool: tool, Decision: "allowed"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, rowID)
	}
	var all eventsAnswer
	if code := sessionGet(t, s, fmt.Sprintf("/api/authoring-sessions/%d/events", session), &all); code != http.StatusOK ||
		len(all.Events) != 3 || all.Next != ids[2] {
		t.Fatalf("answered %d %+v", code, all)
	}
	var later eventsAnswer
	sessionGet(t, s, fmt.Sprintf("/api/authoring-sessions/%d/events?after=%d", session, ids[0]), &later)
	if len(later.Events) != 2 || later.Events[0].RowID != ids[1] || later.Next != ids[2] {
		t.Errorf("after %d: %+v", ids[0], later)
	}
	var none eventsAnswer
	sessionGet(t, s, fmt.Sprintf("/api/authoring-sessions/%d/events?after=%d", session, ids[2]), &none)
	if none.Events == nil || len(none.Events) != 0 || none.Next != ids[2] {
		t.Errorf("nothing new: %+v, want an empty page that keeps the cursor", none)
	}
	if code := sessionGet(t, s, fmt.Sprintf("/api/authoring-sessions/%d/events?since=1", session), nil); code != http.StatusBadRequest {
		t.Errorf("an undeclared term answered %d", code)
	}
}

// The request lists the trees its sessions printed without shipping them; a reader who clicks one
// is sent that tree alone.
func TestThePrintedTreesOfARequestAreListedAndReadOneAtATime(t *testing.T) {
	s, st, _, id := authoringServer(t)
	session, err := st.StartAuthoringSession(id, "operator", "claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	tree := "#10000 UsesStep\n"
	ast, err := st.RecordAuthoringAST(store.AuthoringAST{SessionID: session, QueryID: "acme/a",
		PrintedAt: "2026-09-23T12:00:00Z", Query: "PrintAST", Database: "test/A/A.testproj", Body: tree,
		CLIVersion: "2.27.1"})
	if err != nil {
		t.Fatal(err)
	}

	var listed struct {
		ASTs []store.AuthoringAST `json:"asts"`
	}
	if code := sessionGet(t, s, fmt.Sprintf("/api/query-requests/%d/asts", id), &listed); code != http.StatusOK ||
		len(listed.ASTs) != 1 || listed.ASTs[0].Body != "" || listed.ASTs[0].Size != len(tree) {
		t.Fatalf("listed %d %+v", code, listed)
	}
	var one store.AuthoringAST
	if code := sessionGet(t, s, fmt.Sprintf("/api/authoring-asts/%d", ast), &one); code != http.StatusOK ||
		one.Body != tree || one.RequestID != id {
		t.Errorf("read %d %+v", code, one)
	}
	if code := sessionGet(t, s, fmt.Sprintf("/api/authoring-asts/%d", ast+1), nil); code != http.StatusNotFound {
		t.Errorf("a tree nobody printed answered %d", code)
	}
	var none struct {
		ASTs []store.AuthoringAST `json:"asts"`
	}
	if sessionGet(t, s, "/api/query-requests/999/asts", &none); none.ASTs == nil {
		t.Error("no trees answered null rather than an empty list")
	}
}
