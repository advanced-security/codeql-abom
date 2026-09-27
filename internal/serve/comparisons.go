package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// ComparisonExecutor sets a recorded comparison's run against the code scanning alerts GitHub
// holds, member by member.
//
// An interface so the server does not depend on GitHub's client, and so a test can drive the
// handler without one. Nil means this server cannot read alerts, which the reader is told.
type ComparisonExecutor interface {
	Compare(ctx context.Context, id int64) error
}

// refusedNoAlertReader is what a server built without a GitHub client answers.
const refusedNoAlertReader = "this server cannot read code scanning alerts, so it cannot compare a " +
	"run with them"

// startComparison records that somebody asked for a run's results to be set against the alerts,
// and carries it out off the request.
//
// Guarded like a write: it records what it read, and it spends the operator's GitHub budget. It
// reads GitHub and never writes to it.
func (s *Server) startComparison(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	runID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "a run is named by a number")
		return
	}
	var body struct {
		By string `json:"by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return
	}
	if _, err := s.Store.AnalysisRun(runID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if s.Comparer == nil {
		writeError(w, http.StatusServiceUnavailable, refusedNoAlertReader)
		return
	}
	if s.runUnderWay(runID) {
		writeError(w, http.StatusConflict,
			"this run is under way, and a comparison of results about to change would describe nothing")
		return
	}
	if !s.holdComparison(runID) {
		writeError(w, http.StatusConflict, "a comparison of this run is already under way")
		return
	}
	id, err := s.Store.CreateComparison(runID, body.By)
	if err != nil {
		s.releaseComparison(runID)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	comparer := s.Comparer
	// Detached from the request, as a run is: reading a scope's alerts outlives a request, and a
	// closed tab is not somebody asking to stop.
	go func() {
		defer s.releaseComparison(runID)
		if err := comparer.Compare(context.Background(), id); err != nil && s.Log != nil {
			fmt.Fprintf(s.Log, "comparison %d of run %d: %v\n", id, runID, err)
		}
	}()
	s.writeComparison(w, id)
}

// comparisons lists a run's comparisons, newest first and without members.
func (s *Server) comparisons(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "a run is named by a number")
		return
	}
	if _, err := s.Store.AnalysisRun(runID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	list, err := s.Store.ComparisonsOf(runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"comparisons": list}, nil)
}

// comparison reads one comparison with every member, which is where a reader sees what was not
// compared and why.
func (s *Server) comparison(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "a comparison is named by a number")
		return
	}
	s.writeComparison(w, id)
}

func (s *Server) writeComparison(w http.ResponseWriter, id int64) {
	c, err := s.Store.Comparison(id)
	switch {
	case errors.Is(err, store.ErrNoComparison):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, c, nil)
}

// comparisonResults lists what one repository's results were against the alerts.
func (s *Server) comparisonResults(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "a comparison is named by a number")
		return
	}
	repository := r.URL.Query().Get("repository")
	if repository == "" {
		writeError(w, http.StatusBadRequest, "results are read one repository at a time; name one")
		return
	}
	if _, err := s.Store.Comparison(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	results, err := s.Store.ComparisonResults(id, repository)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"repository": repository, "results": results}, nil)
}

// holdComparison claims a run for one comparison at a time in this process: two reading one run's
// alerts at once would spend the budget twice to record the same thing.
func (s *Server) holdComparison(runID int64) bool {
	s.comparingMu.Lock()
	defer s.comparingMu.Unlock()
	if s.comparing == nil {
		s.comparing = map[int64]bool{}
	}
	if s.comparing[runID] {
		return false
	}
	s.comparing[runID] = true
	return true
}

// comparingRun reports whether a comparison of the run is reading in this process.
func (s *Server) comparingRun(runID int64) bool {
	s.comparingMu.Lock()
	defer s.comparingMu.Unlock()
	return s.comparing[runID]
}

func (s *Server) releaseComparison(runID int64) {
	s.comparingMu.Lock()
	defer s.comparingMu.Unlock()
	delete(s.comparing, runID)
}
