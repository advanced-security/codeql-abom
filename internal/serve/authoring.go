package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/harness"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Authoring runs agent sessions on query requests.
//
// An interface so the server does not need a Copilot CLI to start, and so a test can drive the
// handlers without one. Nil answers that this server has no pack to author in.
type Authoring interface {
	// Start asks for a session on a request; decision is the digest of the signed decision that
	// asked, which a session that waits its turn is checked against as it begins.
	Start(request int64, by, decision string) (store.AuthoringSession, error)
	Stop(request int64) bool
	InFlight(request int64) []harness.Flight
	// Draft asks for a session drafting a request from a run's picked results, decided as Start is.
	Draft(run int64, queryID string, picked []harness.Picked, by, decision string) (store.AuthoringSession, error)
	StopDraft(run int64) bool
}

// startSession begins an agent session on a request, answering once it is under way.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	if s.Authoring == nil {
		writeError(w, http.StatusBadRequest, "this server has no query pack to author in; "+
			"start it with --custom-ql-dir")
		return
	}
	// A session holds CodeQL for its whole length, so a crawl that is on would stall under it, unless
	// sessions work elsewhere, each in its own Job.
	if state := s.crawlHoldingCodeQL(); state != "" && !s.SessionsElsewhere {
		writeError(w, http.StatusConflict, fmt.Sprintf("discovery is %s, and a session holds CodeQL "+
			"for as long as it works; pause discovery first", state))
		return
	}
	var body struct {
		By string `json:"by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return
	}
	body.By = s.actor(r, body.By)
	var decision string
	if who, ok := authz.RequesterOf(r.Context()); ok {
		decision = who.Route
	}
	session, err := s.Authoring.Start(id, body.By, decision)
	switch {
	case errors.Is(err, store.ErrNoQueryRequest):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, harness.ErrBusy), errors.Is(err, store.ErrSessionUnderWay):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, session, nil)
}

// stopSession ends the session working a request.
func (s *Server) stopSession(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	if s.Authoring == nil || !s.Authoring.Stop(id) {
		// A session that ended between the page rendering and the press is the ordinary case.
		writeError(w, http.StatusConflict, fmt.Sprintf("no session is working request %d on this server", id))
		return
	}
	s.requestSessions(w, r)
}

// requestSessions lists a request's sessions, and the CodeQL its live one has under way.
func (s *Server) requestSessions(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	sessions, err := s.Store.AuthoringSessions(id)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	flights := []harness.Flight{}
	if s.Authoring != nil {
		flights = append(flights, s.Authoring.InFlight(id)...)
	}
	writeJSON(w, map[string]any{"sessions": sessions, "inFlight": flights}, nil)
}

// requestASTs lists the trees a request's sessions printed, without the trees.
func (s *Server) requestASTs(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	asts, err := s.Store.AuthoringASTs(id)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, map[string]any{"asts": asts}, nil)
}

// authoringAST sends one printed tree.
func (s *Server) authoringAST(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "a printed tree is named by a number")
		return
	}
	ast, err := s.Store.AuthoringAST(id)
	switch {
	case errors.Is(err, store.ErrNoAuthoringAST):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, ast, nil)
}

// sha256Hex is a digest as an event names a printed tree.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// authoringASTByDigest sends the tree a session printed that one of its events names by digest.
func (s *Server) authoringASTByDigest(w http.ResponseWriter, r *http.Request) {
	session, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || session <= 0 {
		writeError(w, http.StatusBadRequest, "a session is named by a number")
		return
	}
	digest := r.PathValue("digest")
	if !sha256Hex.MatchString(digest) {
		writeError(w, http.StatusBadRequest, "a printed tree is named by the SHA-256 of what it says, in 64 hex digits")
		return
	}
	ast, err := s.Store.AuthoringASTByDigest(session, digest)
	switch {
	case errors.Is(err, store.ErrNoAuthoringAST):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, ast, nil)
}

func requestID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "a request is named by a number")
		return 0, false
	}
	return id, true
}
