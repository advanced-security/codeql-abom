package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// fakeDiscovery records whether the loop was actually asked to do anything.
type fakeDiscovery struct {
	started bool
	paused  bool
	woken   bool
	problem *supervisor.Problem

	selection  expand.Selection
	selectErr  error
	staleAsked bool
	repoAsked  string
}

func (f *fakeDiscovery) Status() supervisor.Status {
	return supervisor.Status{State: supervisor.StatePaused}
}

func (f *fakeDiscovery) Start() *supervisor.Problem {
	if f.problem != nil {
		return f.problem
	}
	f.started = true
	return nil
}

func (f *fakeDiscovery) Pause() { f.paused = true }

func (f *fakeDiscovery) Wake() { f.woken = true }

func (f *fakeDiscovery) RequeueStale(context.Context) (expand.Selection, error) {
	f.staleAsked = true
	return f.selection, f.selectErr
}

func (f *fakeDiscovery) RequeueRepository(name string) (expand.Selection, error) {
	f.repoAsked = name
	return f.selection, f.selectErr
}

func controlRequest(target string) *http.Request {
	r := localRequest(http.MethodPost, target, nil)
	r.Host = "127.0.0.1:8080"
	r.Header.Set(controlHeader, "1")
	return r
}

// A handler answers the caller and nothing else, so a query that breaks for every reader leaves
// no trace on the machine actually running it.
func TestAFailedRequestIsRecordedServerSide(t *testing.T) {
	s, _ := newServer(t)
	var log bytes.Buffer
	s.Log = &log

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, localRequest(http.MethodGet, "/api/nothing-here", nil))

	if !strings.Contains(log.String(), "/api/nothing-here") {
		t.Errorf("log = %q, want the failing request recorded", log.String())
	}
}

// The interface polls continuously. A line per successful request would bury every failure.
func TestSuccessfulRequestsAreNotLogged(t *testing.T) {
	s, _ := newServer(t)
	var log bytes.Buffer
	s.Log = &log

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, localRequest(http.MethodGet, "/api/summary", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if log.Len() != 0 {
		t.Errorf("log = %q, want nothing for a request that worked", log.String())
	}
}

func TestDiscoveryStartsWhenAskedLocally(t *testing.T) {
	s, _ := newServer(t)
	loop := &fakeDiscovery{}
	s.Discovery = loop

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, controlRequest("/api/discovery/start"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !loop.started {
		t.Error("the loop was never asked to start")
	}
}

// Starting discovery makes this process clone repositories and run CodeQL. A page on another
// origin must not be able to spend that on the operator's behalf.
func TestDiscoveryControlRejectsCrossSiteRequests(t *testing.T) {
	s, _ := newServer(t)
	loop := &fakeDiscovery{}
	s.Discovery = loop

	r := controlRequest("/api/discovery/start")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if loop.started {
		t.Error("a cross-site request started discovery")
	}
}

// A cross-site form post cannot set a custom header without a preflight, so requiring one keeps
// the simplest version of this attack from ever reaching the handler.
func TestDiscoveryControlRequiresItsHeader(t *testing.T) {
	s, _ := newServer(t)
	loop := &fakeDiscovery{}
	s.Discovery = loop

	r := localRequest(http.MethodPost, "/api/discovery/start", nil)
	r.Host = "127.0.0.1:8080"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if loop.started {
		t.Error("a request without the control header started discovery")
	}
}

// Re-processing a tier spends the machine's time on clones and database builds, exactly like
// starting discovery, so it is guarded exactly like starting discovery.
func TestRequeueingATierIsGuardedLikeStarting(t *testing.T) {
	s, _ := newServer(t)
	s.Discovery = &fakeDiscovery{}

	r := localRequest(http.MethodPost, "/api/discovery/requeue/direct", nil)
	r.Host = "127.0.0.1:8080"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 without the control header", w.Code)
	}
}

// A tier nobody defined must be refused. Silently matching nothing would report success for a
// re-run that never happened.
func TestRequeueingAnUnknownTierIsRefused(t *testing.T) {
	s, _ := newServer(t)
	s.Discovery = &fakeDiscovery{}

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, controlRequest("/api/discovery/requeue/everything"))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// The loop sleeps between cycles, so requeued work would sit untouched until the interval
// elapsed. Waking it is what makes the control appear to do something.
func TestRequeueingWakesTheLoopWhenThereIsWork(t *testing.T) {
	s, store := newServer(t)
	loop := &fakeDiscovery{}
	s.Discovery = loop
	if _, err := store.DB().Exec(`
		INSERT INTO abom_frontier (node_key, owner, repo, depth, status, score, enqueued_at)
		VALUES ('a/one','a','one',1,'done',1,'2026-01-01')`); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, controlRequest("/api/discovery/requeue/direct"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !loop.woken {
		t.Error("the loop was not woken, so the requeued work waits out the interval")
	}
}

// Nothing requeued means nothing to wake for, and a loop woken for no reason burns a cycle
// discovering there is still nothing to do.
func TestRequeueingNothingLeavesTheLoopAlone(t *testing.T) {
	s, _ := newServer(t)
	loop := &fakeDiscovery{}
	s.Discovery = loop

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, controlRequest("/api/discovery/requeue/transitive"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if loop.woken {
		t.Error("the loop was woken with nothing requeued")
	}
}

// The Host header is what a rebinding attack controls, so a name that is not this machine must
// not be trusted merely because the socket is local.
func TestDiscoveryControlRefusesNonLoopbackByDefault(t *testing.T) {
	s, _ := newServer(t)
	loop := &fakeDiscovery{}
	s.Discovery = loop

	r := controlRequest("/api/discovery/start")
	r.Host = "abom.internal.example"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if loop.started {
		t.Error("a request for another host started discovery")
	}
}

func TestDiscoveryControlAllowsRemoteWhenOperatorOptedIn(t *testing.T) {
	s, _ := newServer(t)
	loop := &fakeDiscovery{}
	s.Discovery = loop
	s.AllowRemoteControl = true

	r := controlRequest("/api/discovery/start")
	r.Host = "abom.internal.example"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !loop.started {
		t.Error("the loop was never asked to start")
	}
}

func requeueResult(t *testing.T, w *httptest.ResponseRecorder) RequeueResult {
	t.Helper()
	var got RequeueResult
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
	return got
}

// Selecting what is out of date spends the same clones and database builds a tier requeue does,
// so it is guarded the same way.
func TestRequeueingStaleWorkIsGuardedLikeStarting(t *testing.T) {
	s, _ := newServer(t)
	loop := &fakeDiscovery{}
	s.Discovery = loop

	r := localRequest(http.MethodPost, "/api/discovery/requeue-stale", nil)
	r.Host = "127.0.0.1:8080"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 without the control header", w.Code)
	}
	if loop.staleAsked {
		t.Error("an unguarded request reached the selection")
	}
}

// The count alone cannot be checked by anyone. What it was judged against is the difference
// between a number and a claim, and only the loop that resolved the packs can say.
func TestRequeueingStaleWorkReportsWhatItJudgedAgainst(t *testing.T) {
	s, _ := newServer(t)
	s.Discovery = &fakeDiscovery{
		selection: expand.Selection{Requeued: 1742, CLIVersion: "2.26.4", Packs: 4},
	}

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, controlRequest("/api/discovery/requeue-stale"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	got := requeueResult(t, w)
	if got.Requeued != 1742 || got.CLIVersion != "2.26.4" || got.Packs != 4 {
		t.Errorf("got %+v, want the selection the loop reported", got)
	}
}

// The state travels with the numbers. Without it the queue counts beside the button stay a poll
// behind, so a requeue of 1,742 items leaves "queued 0" on screen.
func TestASelectiveRequeueAnswersWithTheStateItLeftBehind(t *testing.T) {
	s, _ := newServer(t)
	s.Discovery = &fakeDiscovery{selection: expand.Selection{Requeued: 1}}

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, controlRequest("/api/discovery/requeue-stale"))

	if got := requeueResult(t, w); !got.State.Available {
		t.Error("the answer carried no discovery state, so the page cannot refresh from it")
	}
}

// Requeued work sits untouched until the interval elapses, which reads as a control that did
// nothing for as long as the loop sleeps.
func TestRequeueingStaleWorkWakesTheLoop(t *testing.T) {
	s, _ := newServer(t)
	loop := &fakeDiscovery{selection: expand.Selection{Requeued: 3}}
	s.Discovery = loop

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, controlRequest("/api/discovery/requeue-stale"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !loop.woken {
		t.Error("the loop was not woken, so the requeued work waits out the interval")
	}
}

// A corpus that is entirely current is the normal outcome after a crawl, and waking the loop for
// it burns a cycle discovering there is nothing to do.
func TestRequeueingNothingStaleLeavesTheLoopAlone(t *testing.T) {
	s, _ := newServer(t)
	loop := &fakeDiscovery{}
	s.Discovery = loop

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, controlRequest("/api/discovery/requeue-stale"))

	if loop.woken {
		t.Error("the loop was woken with nothing requeued")
	}
}

// A repository is named in the body rather than the path: an owner and a name are two segments,
// and routing on them would collide with the tier route beside it.
func TestRequeueingARepositoryUsesTheNameItWasGiven(t *testing.T) {
	s, _ := newServer(t)
	loop := &fakeDiscovery{selection: expand.Selection{Requeued: 12, Expedited: 12}}
	s.Discovery = loop

	r := controlRequest("/api/discovery/requeue-repository")
	r.Body = io.NopCloser(strings.NewReader(`{"repository":"octo/mine"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if loop.repoAsked != "octo/mine" {
		t.Errorf("the loop was asked for %q, want octo/mine", loop.repoAsked)
	}
	if got := requeueResult(t, w); got.Expedited != 12 {
		t.Errorf("expedited %d, want the 12 the loop reported", got.Expedited)
	}
}

// An empty name matches nothing, and reporting success for it leaves an operator watching a
// crawl that was never going to run.
func TestRequeueingARepositoryNeedsAName(t *testing.T) {
	s, _ := newServer(t)
	loop := &fakeDiscovery{}
	s.Discovery = loop

	r := controlRequest("/api/discovery/requeue-repository")
	r.Body = io.NopCloser(strings.NewReader(`{"repository":"  "}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if loop.repoAsked != "" {
		t.Errorf("the loop was asked for %q on an empty name", loop.repoAsked)
	}
}

// A server built without a loop has nothing to resolve the packs, so it cannot judge staleness
// at all. Answering 0 would report the corpus as current.
func TestSelectiveRequeueNeedsADiscoveryLoop(t *testing.T) {
	s, _ := newServer(t)
	s.Discovery = nil

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, controlRequest("/api/discovery/requeue-stale"))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

// Retrying unavailable work spends clones like any requeue, so it is guarded the same way.
func TestRetryingUnavailableWorkIsGuardedLikeStarting(t *testing.T) {
	s, store := newServer(t)
	s.Discovery = &fakeDiscovery{}
	seedUnavailable(t, store, "octo", "private")

	r := localRequest(http.MethodPost, "/api/discovery/retry-unavailable", nil)
	r.Host = "127.0.0.1:8080"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 without the control header", w.Code)
	}
	var status string
	if err := store.DB().QueryRow(
		`SELECT status FROM abom_frontier WHERE node_key = 'octo/private'`).Scan(&status); err != nil {
		t.Fatalf("reading row: %v", err)
	}
	if status != "unavailable" {
		t.Errorf("a refused request changed the row to %q", status)
	}
}

// The answer says both what was put back and what could not be, or an operator reading "1
// requeued" beside "2 unavailable" cannot tell whether the retry failed.
func TestRetryingUnavailableWorkReportsWhatItCouldNotPutBack(t *testing.T) {
	s, store := newServer(t)
	loop := &fakeDiscovery{}
	s.Discovery = loop
	seedUnavailable(t, store, "octo", "private")
	seedUnavailable(t, store, "${{ github.repository }}", "setup")

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, controlRequest("/api/discovery/retry-unavailable"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	got := requeueResult(t, w)
	if got.Requeued != 1 || got.NamesNoRepository != 1 {
		t.Errorf("requeued=%d namesNoRepository=%d, want 1 and 1",
			got.Requeued, got.NamesNoRepository)
	}
	if !loop.woken {
		t.Error("the loop was not woken, so the retried work waits out the interval")
	}
}

func TestRetryingWhenNothingIsUnavailableLeavesTheLoopAlone(t *testing.T) {
	s, _ := newServer(t)
	loop := &fakeDiscovery{}
	s.Discovery = loop

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, controlRequest("/api/discovery/retry-unavailable"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if loop.woken {
		t.Error("the loop was woken with nothing retried")
	}
}

func seedUnavailable(t *testing.T, st *store.Store, owner, repo string) {
	t.Helper()
	if _, err := st.DB().Exec(`
		INSERT INTO abom_frontier (node_key, owner, repo, depth, status, attempts, score,
			enqueued_at, reason)
		VALUES (?, ?, ?, 1, 'unavailable', 3, 1, '2026-01-01', 'fatal: could not read')`,
		owner+"/"+repo, owner, repo); err != nil {
		t.Fatalf("seeding %s/%s: %v", owner, repo, err)
	}
}

// Reading what discovery is doing is not controlling it, and the Discovery view polls it
// constantly.
func TestDiscoveryStatusIsReadableWithoutTheControlGuard(t *testing.T) {
	s, _ := newServer(t)
	s.Discovery = &fakeDiscovery{}

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, localRequest(http.MethodGet, "/api/discovery/status", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

// A server built without a loop still answers, so the interface can say discovery is
// unavailable rather than showing a broken page.
func TestDiscoveryStatusAnswersWhenNoLoopIsConfigured(t *testing.T) {
	s, _ := newServer(t)

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, localRequest(http.MethodGet, "/api/discovery/status", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
}
