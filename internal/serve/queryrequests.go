package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// requestRow is a request with the steps toward it: (a) the target, then the target's working
// copy, or every step waiting when nothing on disk implements it.
type requestRow struct {
	store.QueryRequest
	Steps   []authoring.Step  `json:"steps"`
	Outcome authoring.Outcome `json:"outcome"`
}

// queryRequests lists what people have asked queries to do, with the words a form may offer.
// A done or declined request shows the steps it closed on, since a later request may change its
// query.
func (s *Server) queryRequests(w http.ResponseWriter, r *http.Request) {
	requests, err := s.Store.QueryRequests()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	byID, err := s.stepsByQuery()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	running, err := s.Store.RunningAuthoringSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows := make([]requestRow, 0, len(requests))
	for _, req := range requests {
		row := requestRow{QueryRequest: req}
		if req.ClosedSteps == "" {
			row.Steps = requestSteps(req, byID)
		} else if err := json.Unmarshal([]byte(req.ClosedSteps), &row.Steps); err != nil {
			writeError(w, http.StatusInternalServerError,
				fmt.Sprintf("reading the steps request %d closed on: %v", req.RowID, err))
			return
		}
		_, agentWorking := running[req.RowID]
		row.Outcome = authoring.Overall(row.Steps, agentWorking)
		rows = append(rows, row)
	}
	writeJSON(w, map[string]any{
		"requests":   rows,
		"kinds":      store.QueryRequestKinds(),
		"statuses":   store.QueryRequestStatuses(),
		"precisions": store.QueryRequestPrecisions(),
		"severities": store.QueryRequestSeverities(),
	}, nil)
}

// saveQueryRequest keeps a new request.
func (s *Server) saveQueryRequest(w http.ResponseWriter, r *http.Request) {
	var body store.QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return
	}
	body.RowID = 0
	s.keepQueryRequest(w, r, body)
}

// editQueryRequest changes what the request names asks for.
func (s *Server) editQueryRequest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "a request is named by a number")
		return
	}
	var body store.QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return
	}
	body.RowID = id
	s.keepQueryRequest(w, r, body)
}

// deleteQueryRequest removes a request filed wrongly, with its sessions and their timelines.
func (s *Server) deleteQueryRequest(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "a request is named by a number")
		return
	}
	switch err := s.Store.DeleteQueryRequest(id); {
	case errors.Is(err, store.ErrNoQueryRequest):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, store.ErrSessionUnderWay), errors.Is(err, store.ErrRequestWorked):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.queryRequests(w, r)
}

func (s *Server) keepQueryRequest(w http.ResponseWriter, r *http.Request, body store.QueryRequest) {
	// Saved here, a request is the operator's act, so they chose its target whatever the body
	// says; the agent records its own decisions.
	body.TargetSource, body.TargetDecision = "", ""
	if body.QueryID != "" {
		decision, refusal, err := s.targetDecision(body.QueryID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if refusal != "" {
			writeError(w, http.StatusBadRequest, refusal)
			return
		}
		body.TargetSource, body.TargetDecision = store.TargetOperator, decision
	}
	if status, err := s.readExamples(r.Context(), &body); err != nil {
		writeError(w, status, err.Error())
		return
	}
	if body.Status == store.RequestDone || body.Status == store.RequestDeclined {
		byID, err := s.stepsByQuery()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		closed, err := json.Marshal(requestSteps(body, byID))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		body.ClosedSteps = string(closed)
	}
	if _, err := s.Store.SaveQueryRequest(body); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrNoQueryRequest) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.queryRequests(w, r)
}

// stepsByQuery is the working copy's steps for each query id on disk.
func (s *Server) stepsByQuery() (map[string][]authoring.Step, error) {
	working, steps, err := s.workingCopy()
	if err != nil {
		return nil, err
	}
	byID := map[string][]authoring.Step{}
	for _, q := range working {
		byID[q.QueryID] = steps[q.QueryID+"\x00"+q.QueryVersion]
	}
	return byID, nil
}

// requestSteps is (a) the target, then its working copy's steps, or every step waiting when
// nothing on disk implements it.
func requestSteps(req store.QueryRequest, byID map[string][]authoring.Step) []authoring.Step {
	out := []authoring.Step{authoring.TargetStep(req)}
	progress, onDisk := byID[req.QueryID]
	switch {
	case req.QueryID == "":
		return append(out, authoring.Unstarted("no query chosen yet")...)
	case onDisk:
		return append(out, progress...)
	}
	return append(out, authoring.Unstarted(
		"nothing in the query directory implements "+req.QueryID+" yet")...)
}

// targetDecision says what naming id asks for: to improve a query the operator's pack holds, or to
// create one nothing holds. It refuses, saying why, an id no session could serve.
func (s *Server) targetDecision(id string) (string, string, error) {
	working, _, err := s.workingCopy()
	if err != nil {
		return "", "", err
	}
	catalog, err := s.Store.QueryCatalog(working...)
	if err != nil {
		return "", "", err
	}
	inPack := slices.ContainsFunc(working, func(q store.WorkingQuery) bool { return q.QueryID == id })
	for _, q := range catalog {
		switch {
		case q.QueryID != id:
		case q.Lane || q.Discovery:
			return "", id + " builds ABOM's own graph or runs a whole suite, and is not a query a request changes", nil
		case inPack:
			return store.DecisionImprove, "", nil
		default:
			return "", fmt.Sprintf("%s is not in your query pack, and a session writes only there; "+
				"ask for a new query with an id of your own instead", id), nil
		}
	}
	if !customql.ValidQueryID(id) {
		return "", fmt.Sprintf("%q is not a query id such as acme/checkout-tag-pin: lower-case "+
			"words joined by hyphens, owner first, parts separated by /", id), nil
	}
	return store.DecisionCreate, "", nil
}
