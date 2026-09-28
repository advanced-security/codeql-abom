package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/lockverify"
)

// verifyLock runs GitHub's lock verification of one repository's current commit, because
// somebody asked. The only path that runs the tool: no read ever does.
func (s *Server) verifyLock(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	if s.VerifyLock == nil {
		writeError(w, http.StatusServiceUnavailable, "lock verification is not available on this server")
		return
	}
	var body struct {
		Repository string `json:"repository"`
		Mode       string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest,
			`expected a JSON object naming a repository and a mode, "local" or "network"`)
		return
	}
	if strings.TrimSpace(body.Repository) == "" {
		writeError(w, http.StatusBadRequest, "name a repository as owner/name")
		return
	}
	got, err := s.VerifyLock(r.Context(), strings.TrimSpace(body.Repository), body.Mode)
	switch {
	case errors.Is(err, lockverify.ErrNothingToVerify):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, lockverify.ErrTooSoon), errors.Is(err, lockverify.ErrBudget):
		writeError(w, http.StatusTooManyRequests, err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, got, nil)
	}
}

// lockVerifications lists one repository's verifications, newest first. A read: it never runs
// the tool.
func (s *Server) lockVerifications(w http.ResponseWriter, r *http.Request) {
	repository := strings.TrimSpace(r.URL.Query().Get("repository"))
	if repository == "" {
		writeError(w, http.StatusBadRequest, "name a repository as owner/name")
		return
	}
	got, err := s.Store.LockVerifications(repository, 50)
	writeJSON(w, got, err)
}
