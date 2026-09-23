package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// requestRow is a request with the steps toward it: (a) the target, then the target's working
// copy, or every step waiting when nothing on disk implements it.
type requestRow struct {
	store.QueryRequest
	Steps []authoring.Step `json:"steps"`
}

// queryRequests lists what people have asked queries to do, with the words a form may offer.
func (s *Server) queryRequests(w http.ResponseWriter, r *http.Request) {
	requests, err := s.Store.QueryRequests()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	working, steps, err := s.workingCopy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	byID := map[string][]authoring.Step{}
	for _, q := range working {
		byID[q.QueryID] = steps[q.QueryID+"\x00"+q.QueryVersion]
	}
	rows := make([]requestRow, 0, len(requests))
	for _, req := range requests {
		row := requestRow{QueryRequest: req, Steps: []authoring.Step{authoring.TargetStep(req)}}
		progress, onDisk := byID[req.QueryID]
		switch {
		case req.QueryID == "":
			row.Steps = append(row.Steps, authoring.Unstarted("no query chosen yet")...)
		case onDisk:
			row.Steps = append(row.Steps, progress...)
		default:
			row.Steps = append(row.Steps, authoring.Unstarted(
				"nothing in the query directory implements "+req.QueryID+" yet")...)
		}
		rows = append(rows, row)
	}
	writeJSON(w, map[string]any{
		"requests": rows,
		"kinds":    store.QueryRequestKinds(),
		"statuses": store.QueryRequestStatuses(),
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

func (s *Server) keepQueryRequest(w http.ResponseWriter, r *http.Request, body store.QueryRequest) {
	// Saved here, a request is the operator's act, so they chose its target whatever the body
	// says; the agent records its own decisions.
	body.TargetSource = ""
	if body.QueryID != "" {
		known, err := s.knownQuery(body.QueryID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !known {
			writeError(w, http.StatusBadRequest, fmt.Sprintf(
				"%s is not a query ABOM holds; choose one from the catalog, or leave it to the agent",
				body.QueryID))
			return
		}
		body.TargetSource = store.TargetOperator
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

// knownQuery reports whether a request may name id: a query the catalog or the operator's pack
// holds, other than one that builds the graph or a whole-suite lane.
func (s *Server) knownQuery(id string) (bool, error) {
	working, _, err := s.workingCopy()
	if err != nil {
		return false, err
	}
	catalog, err := s.Store.QueryCatalog(working...)
	if err != nil {
		return false, err
	}
	for _, q := range catalog {
		if q.QueryID == id && !q.Lane && !q.Discovery {
			return true, nil
		}
	}
	return false, nil
}
