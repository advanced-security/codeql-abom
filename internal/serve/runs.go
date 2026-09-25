package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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
// The predicate arrives in the query string rather than the body so it is parsed by
// `graphPredicate`, exactly as every other surface parses one, and resolved by `runSet`, exactly
// as /api/databases previews it. A reader is shown the set a run will cover before pressing it.
//
// It records and does not execute. Keeping those apart is what lets a reader see the size of a
// thing before it happens.
func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	spec, err := s.runPredicate(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var body struct {
		Queries []store.AnalysisQuery `json:"queries"`
		By      string                `json:"by"`
		// Held keeps what the queries report apart from findings; absent takes the suggestion.
		Held *bool `json:"held"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return
	}
	held := body.Held != nil && *body.Held
	if body.Held == nil && len(body.Queries) > 0 {
		suggested, err := s.Store.SuggestFindings(body.Queries)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		held = !suggested.Findings
	}

	set, err := s.runSet(r.Context(), spec)
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
		// The question a name resolved to, never the name, which another scope can later take.
		Predicate: spec.String(),
		// And the name beside it, as how the run was chosen; runPredicate refused an unknown one.
		ScopeName: r.URL.Query().Get(viewNameParam),
		Mode:      set.Mode,
		Held:      held,
		Queries:   body.Queries,
		Databases: set.Databases,
		Gaps:      set.Gaps,
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

// runResults lists what one member of a run reported, optionally for one query, so a reader can
// pick the results to push back on.
func (s *Server) runResults(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "a run is named by a number")
		return
	}
	repository := r.URL.Query().Get("repository")
	if repository == "" {
		writeError(w, http.StatusBadRequest, "results are read one repository at a time; name one")
		return
	}
	if _, err := s.Store.AnalysisRun(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	found, err := s.Store.RunFindings(id, repository, r.URL.Query().Get("query"), runResultsLimit)
	switch {
	case errors.Is(err, store.ErrNoRunMember):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, found, nil)
}

// runResultsLimit bounds one member's listing; past it the answer says it was cut short.
const runResultsLimit = 500

// runSuggestion says whether a run of the named query versions should store findings, each named
// as id@version, so a draft shows the setting and its reason before Run.
func (s *Server) runSuggestion(w http.ResponseWriter, r *http.Request) {
	named := r.URL.Query()["query"]
	if len(named) == 0 {
		writeError(w, http.StatusBadRequest, "name each query version as query=id@version")
		return
	}
	queries := make([]store.AnalysisQuery, 0, len(named))
	for _, n := range named {
		at := strings.LastIndex(n, "@")
		if at <= 0 || at == len(n)-1 {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("%q names no version; name it as query=id@version", n))
			return
		}
		queries = append(queries, store.AnalysisQuery{QueryID: n[:at], QueryVersion: n[at+1:]})
	}
	suggested, err := s.Store.SuggestFindings(queries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, suggested, nil)
}

// convertHeld makes findings of what a held run kept: the whole run, one member, or the results
// named in it. Guarded like any write, since every surface reads what it adds.
func (s *Server) convertHeld(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "a run is named by a number")
		return
	}
	var body struct {
		Repository string  `json:"repository"`
		Results    []int64 `json:"results"`
		By         string  `json:"by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return
	}
	if _, err := s.Store.AnalysisRun(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	converted, err := s.Store.ConvertHeld(id, body.Repository, body.Results, body.By)
	switch {
	case errors.Is(err, store.ErrNoRunMember):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, converted, nil)
}

// findingTrace sends one finding with every evaluation that reported it.
func (s *Server) findingTrace(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "a finding is named by a number")
		return
	}
	trace, err := s.Store.FindingTrace(id)
	switch {
	case errors.Is(err, store.ErrNoFinding):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, trace, nil)
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
// The executor returns the member in flight to pending and records `canceled`, so what has already
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
