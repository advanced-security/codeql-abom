package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/advanced-security/codeql-abom/internal/supervisor"
	"github.com/advanced-security/codeql-abom/internal/toolchain"
)

// Toolchain reports what an authoring session needs and names the CLIs it runs.
type Toolchain interface {
	Checks(ctx context.Context, fresh bool) []toolchain.Check
	SetTool(ctx context.Context, tool, path, by string) error
}

// keyCodeQLFree is the check that no crawl holds CodeQL, which a session needs for its whole length.
const keyCodeQLFree = "codeql-free"

// crawlHoldingCodeQL names the discovery state that would refuse a session, or "" when none would.
func (s *Server) crawlHoldingCodeQL() string {
	if s.Discovery == nil {
		return ""
	}
	if state := s.Discovery.Status().State; state != supervisor.StatePaused && state != supervisor.StateStopped {
		return state
	}
	return ""
}

// authoringReadiness says whether a session could start, and for each thing it needs, why not.
func (s *Server) authoringReadiness(w http.ResponseWriter, r *http.Request) {
	if s.Toolchain == nil {
		writeJSON(w, map[string]any{"known": false, "ready": false, "checks": []toolchain.Check{}}, nil)
		return
	}
	checks := s.Toolchain.Checks(r.Context(), r.URL.Query().Get("fresh") != "")
	if checks == nil {
		checks = []toolchain.Check{}
	}
	if s.Discovery != nil {
		free := toolchain.Check{Key: keyCodeQLFree, Name: "CodeQL free for a session", State: toolchain.Ready}
		if state := s.crawlHoldingCodeQL(); state != "" {
			free.State = toolchain.Problem
			free.Reason = fmt.Sprintf("discovery is %s, and a session holds CodeQL for as long as it works", state)
			free.Remedy = "pause discovery on the Discovery page, then start the session"
		}
		checks = append(checks, free)
	}
	ready := true
	for _, c := range checks {
		ready = ready && c.State == toolchain.Ready
	}
	writeJSON(w, map[string]any{"known": true, "ready": ready, "checks": checks}, nil)
}

// setTool names the executable a tool runs, checked before it is kept.
func (s *Server) setTool(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	if s.Toolchain == nil {
		writeError(w, http.StatusConflict, "this server cannot change the tools it runs")
		return
	}
	var body struct {
		Path string `json:"path"`
		By   string `json:"by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return
	}
	switch err := s.Toolchain.SetTool(r.Context(), r.PathValue("tool"), body.Path, body.By); {
	case errors.Is(err, toolchain.ErrUnusable):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.authoringReadiness(w, r)
}
