package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/harness"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// startDraft begins a session drafting a request from results an operator picked out of a run.
// Nothing is filed: the operator files the draft, edits it or discards it.
func (s *Server) startDraft(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	run, ok := runNamed(w, r)
	if !ok {
		return
	}
	if s.Authoring == nil {
		writeError(w, http.StatusBadRequest, "this server has no agent to draft with; start it with "+
			"--custom-ql-dir and a Copilot CLI named by --copilot-cli")
		return
	}
	var body struct {
		QueryID  string                  `json:"queryId"`
		By       string                  `json:"by"`
		Evidence []store.RequestEvidence `json:"evidence"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return
	}
	switch {
	case strings.TrimSpace(body.By) == "":
		writeError(w, http.StatusBadRequest, "a draft records who asked for it; sign in with gh")
		return
	case len(body.Evidence) == 0:
		writeError(w, http.StatusBadRequest, "a draft is written from picked results; pick one")
		return
	}
	for i := range body.Evidence {
		body.Evidence[i].RunID = run
	}
	if err := s.Store.CheckPicked(run, body.QueryID, body.Evidence); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	picked, err := s.picked(run, body.QueryID, body.Evidence)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	session, err := s.Authoring.Draft(run, body.QueryID, picked, body.By)
	switch {
	case errors.Is(err, store.ErrSessionUnderWay):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, session, nil)
}

// picked hands each pick on with where the run found it and what it said there.
func (s *Server) picked(run int64, queryID string, evidence []store.RequestEvidence) ([]harness.Picked, error) {
	out := make([]harness.Picked, 0, len(evidence))
	for _, e := range evidence {
		p := harness.Picked{Repository: e.Repository, Kind: e.Kind}
		if e.Kind == store.RequestFalseAlarm {
			member, err := s.Store.RunFindings(run, e.Repository, queryID, 500)
			if err != nil {
				return nil, err
			}
			for _, f := range member.Findings {
				if (e.FindingRowID != 0 && f.FindingRowID == e.FindingRowID) ||
					(e.HeldResultID != 0 && f.HeldResultID == e.HeldResultID) {
					p.FilePath, p.StartLine, p.Message = f.FilePath, f.StartLine, f.Message
				}
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// runDrafts lists the sessions drafting from a run, each with the newest draft it proposed.
func (s *Server) runDrafts(w http.ResponseWriter, r *http.Request) {
	run, ok := runNamed(w, r)
	if !ok {
		return
	}
	drafts, err := s.Store.DraftSessions(run)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"drafts": drafts}, nil)
}

// stopDraft ends the session drafting from a run.
func (s *Server) stopDraft(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	run, ok := runNamed(w, r)
	if !ok {
		return
	}
	if s.Authoring == nil || !s.Authoring.StopDraft(run) {
		writeError(w, http.StatusConflict, fmt.Sprintf("no session is drafting from run %d on this server", run))
		return
	}
	s.runDrafts(w, r)
}

func runNamed(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "a run is named by a number")
		return 0, false
	}
	return id, true
}
