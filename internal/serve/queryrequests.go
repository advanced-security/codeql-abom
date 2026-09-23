package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// queryRequests lists what people have asked queries to do, with the words a form may offer.
func (s *Server) queryRequests(w http.ResponseWriter, r *http.Request) {
	requests, err := s.Store.QueryRequests()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"requests": requests,
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
