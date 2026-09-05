package serve

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// fakeDiscovery records whether the loop was actually asked to do anything.
type fakeDiscovery struct {
	started bool
	paused  bool
	woken   bool
	problem *supervisor.Problem
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

func controlRequest(target string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, target, nil)
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
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/nothing-here", nil))

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
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/summary", nil))

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

	r := httptest.NewRequest(http.MethodPost, "/api/discovery/start", nil)
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

	r := httptest.NewRequest(http.MethodPost, "/api/discovery/requeue/direct", nil)
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

// Reading what discovery is doing is not controlling it, and the Discovery view polls it
// constantly.
func TestDiscoveryStatusIsReadableWithoutTheControlGuard(t *testing.T) {
	s, _ := newServer(t)
	s.Discovery = &fakeDiscovery{}

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/discovery/status", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

// A server built without a loop still answers, so the interface can say discovery is
// unavailable rather than showing a broken page.
func TestDiscoveryStatusAnswersWhenNoLoopIsConfigured(t *testing.T) {
	s, _ := newServer(t)

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/discovery/status", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
}
