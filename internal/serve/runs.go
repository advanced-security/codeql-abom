package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// RunExecutor carries out a recorded run.
//
// An interface so the server does not depend on the CodeQL plumbing, and so a test can drive the
// handler without one. Nil means this server was started without a runner, which is a thing the
// reader is told rather than a fault.
type RunExecutor interface {
	Run(ctx context.Context, runID int64) error
}

// startRun records a request to run an explicit list of queries over the databases a scope
// resolves to.
//
// The predicate arrives in the query string rather than the body so it goes through
// `graphPredicate` and `UnenforceableAsSet` exactly as /api/databases and /api/match do. A second
// way to express a scope is a second policy engine, and the one nobody simulated is the one that
// decides what an expensive run covers.
//
// It records and does not execute. Keeping those apart is what lets a reader see the size of a
// thing before it happens.
func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	spec, err := s.graphPredicate(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsSet(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, unenforceable(refused))
		return
	}

	var body struct {
		Queries []store.AnalysisQuery `json:"queries"`
		By      string                `json:"by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return
	}

	set, err := s.Store.DatabasesMatching(store.SelectorFor(spec))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(set.Databases)+len(set.Gaps) == 0 {
		// The resolution already worked out why, and repeating it here is what lets a reader see
		// an unanswerable scope rather than an empty run.
		refusal := set.Unresolved
		if refusal == "" {
			refusal = "this scope names no repository"
		}
		writeError(w, http.StatusBadRequest, refusal)
		return
	}

	id, err := s.Store.CreateAnalysisRun(store.AnalysisRequest{
		RequestedBy: body.By,
		Predicate:   r.URL.RawQuery,
		Mode:        set.Mode,
		Queries:     body.Queries,
		Databases:   set.Databases,
		Gaps:        set.Gaps,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	run, err := s.Store.AnalysisRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, run, nil)
}

// runs lists what has been asked for, newest first and without members.
func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.AnalysisRuns()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"runs": list}, nil)
}

// run reads one request with every member, which is where a reader sees what it could not cover.
func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "a run is named by a number")
		return
	}
	found, err := s.Store.AnalysisRun(id)
	if err != nil {
		// A run that is not there is the reader asking for one that does not exist, not a fault.
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, found, nil)
}

// executeRun carries out a recorded run, answering as soon as it has started.
//
// Detached from the request on purpose: a run over a corpus outlives any reasonable request, and
// the state a reader polls is the run's own. The background context is deliberate for the same
// reason -- canceling because a browser tab closed would stop work somebody asked for.
func (s *Server) executeRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "a run is named by a number")
		return
	}
	found, err := s.Store.AnalysisRun(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if s.Executor == nil {
		writeError(w, http.StatusBadRequest,
			"this server was started without a CodeQL runner, so it can record a run but not carry one out")
		return
	}
	if found.State == store.RunRunning {
		writeError(w, http.StatusBadRequest, "this run is already under way")
		return
	}

	executor := s.Executor
	ctx, cancel := context.WithCancel(context.Background())
	s.holdRun(id, cancel)
	go func() {
		defer s.releaseRun(id)
		if err := executor.Run(ctx, id); err != nil && s.Log != nil {
			fmt.Fprintf(s.Log, "run %d: %v\n", id, err)
		}
	}()
	writeJSON(w, found, nil)
}

// cancelRun stops a run that is under way.
//
// The executor observes cancellation between members and records `canceled`, so what has already
// been analyzed is kept and the rest is left as it was. A run nobody can stop is minutes of
// CodeQL a reader has to wait out or kill the server to escape.
func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "a run is named by a number")
		return
	}
	if !s.stopRun(id) {
		// Not an error: a run that finished between the page rendering and the press is the
		// ordinary case, and reporting it as a failure sends a reader looking for a fault.
		writeError(w, http.StatusConflict, "this run is not under way on this server")
		return
	}
	s.run(w, r)
}

// holdRun remembers how to stop a run while it is in flight.
func (s *Server) holdRun(id int64, cancel context.CancelFunc) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	if s.running == nil {
		s.running = map[int64]context.CancelFunc{}
	}
	s.running[id] = cancel
}

// releaseRun forgets a run that has stopped, and releases its context either way.
func (s *Server) releaseRun(id int64) {
	s.runsMu.Lock()
	cancel, ok := s.running[id]
	delete(s.running, id)
	s.runsMu.Unlock()
	if ok {
		cancel()
	}
}

// stopRun cancels a run and reports whether there was one to cancel.
//
// Only runs this process started: a run left `running` by a server that died is not one this
// server can stop, and pretending otherwise would report a cancellation that never happened.
func (s *Server) stopRun(id int64) bool {
	s.runsMu.Lock()
	cancel, ok := s.running[id]
	s.runsMu.Unlock()
	if ok {
		cancel()
	}
	return ok
}
