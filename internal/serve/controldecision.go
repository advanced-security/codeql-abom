package serve

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// controlOperations is the operation every API mutation is decided as. A route missing here is
// refused rather than served, and TestEveryAPIMutationIsARegisteredOperation fails the build first.
var controlOperations = map[string]string{
	"PUT /api/owned-scope": authz.OpDeclareScope,

	"POST /api/discovery/start":               authz.OpDiscoveryControl,
	"POST /api/discovery/pause":               authz.OpDiscoveryControl,
	"POST /api/discovery/resume-on-start":     authz.OpDiscoveryControl,
	"POST /api/discovery/requeue/{tier}":      authz.OpDiscoveryControl,
	"POST /api/discovery/requeue-stale":       authz.OpDiscoveryControl,
	"POST /api/discovery/requeue-repository":  authz.OpDiscoveryControl,
	"POST /api/discovery/resync":              authz.OpDiscoveryControl,
	"POST /api/discovery/retry-unavailable":   authz.OpDiscoveryControl,
	"POST /api/owned-scope/enumerate":         authz.OpDiscoveryControl,
	"POST /api/accounts/{owner}/repositories": authz.OpDiscoveryControl,
	"POST /api/accounts/{owner}/properties":   authz.OpDiscoveryControl,

	"POST /api/accounts/refresh": authz.OpVerifyIdentity,
	"POST /api/locks/verify":     authz.OpLockVerify,

	"POST /api/code-scanning/previews":              authz.OpEnableScanning,
	"POST /api/code-scanning/previews/{id}/approve": authz.OpEnableScanning,
	"POST /api/code-scanning/batches":               authz.OpEnableScanning,
	"POST /api/code-scanning/batches/{id}/{action}": authz.OpEnableScanning,

	"PUT /api/pools":                   authz.OpConfigure,
	"PUT /api/discovery/suites/{tier}": authz.OpConfigure,

	"PUT /api/policies":                  authz.OpPolicy,
	"POST /api/policies/{name}/simulate": authz.OpPolicy,
	"POST /api/policies/{name}/adopt":    authz.OpPolicy,
	"POST /api/policies/{name}/waivers":  authz.OpPolicy,

	"POST /api/queries/stage":  authz.OpQueryLifecycle,
	"POST /api/queries/export": authz.OpExportQuery,
	"POST /api/queries/test":   authz.OpQueryTest,
	"POST /api/queries/ast":    authz.OpQueryTest,

	"POST /api/query-requests":         authz.OpQueryRequest,
	"PUT /api/query-requests/{id}":     authz.OpQueryRequest,
	"DELETE /api/query-requests/{id}":  authz.OpQueryRequest,
	"POST /api/query-requests/example": authz.OpQueryRequest,

	"PUT /api/authoring/tools/{tool}":             authz.OpConfigure,
	"POST /api/query-requests/{id}/sessions":      authz.OpAuthoring,
	"POST /api/query-requests/{id}/sessions/stop": authz.OpAuthoring,
	"POST /api/runs/{id}/drafts":                  authz.OpAuthoring,
	"POST /api/runs/{id}/drafts/stop":             authz.OpAuthoring,

	"POST /api/runs":                  authz.OpRequestedRun,
	"POST /api/runs/{id}/convert":     authz.OpRequestedRun,
	"POST /api/runs/{id}/comparisons": authz.OpRequestedRun,
	"POST /api/runs/{id}/start":       authz.OpRequestedRun,
	"POST /api/runs/{id}/cancel":      authz.OpRequestedRun,

	"PUT /api/views/{name}":    authz.OpSavedView,
	"DELETE /api/views/{name}": authz.OpSavedView,

	"POST /api/prune/apply":   authz.OpPrune,
	"POST /api/prune/compact": authz.OpPrune,

	"POST /api/repositories/{owner}/{name}/snapshot/submit": authz.OpSubmitSnapshot,
	"POST /api/submissions/renew":                           authz.OpSubmitSnapshot,
}

// controlled reports whether a route changes something and is therefore decided: an API write,
// except the webhook, which its own signature authenticates, and the two previews, which write
// nothing.
func controlled(pattern string) bool {
	method, path, _ := strings.Cut(pattern, " ")
	if method == http.MethodGet || method == http.MethodHead || !strings.HasPrefix(path, "/api/") {
		return false
	}
	switch pattern {
	case "POST /api/events/github", "POST /api/owned-scope/preview",
		"POST /api/repositories/{owner}/{name}/snapshot/preview":
		return false
	}
	return true
}

// maxControlBody bounds what a control's digest is taken over; no control sends more.
const maxControlBody = 64 << 20

type controlEvidence struct {
	Route string `json:"route"`
	Path  string `json:"path"`
	Query string `json:"query,omitempty"`
	Host  string `json:"host"`
	// Body is the sha256 of exactly what was sent, so the log binds the request, not its route.
	Body string `json:"body"`
}

// recordControl signs and logs the decision to act on a control the boundary admitted, before its
// handler runs, and returns it so a decision the handler makes cites it. A decision that cannot be
// recorded is a control not acted on.
func (s *Server) recordControl(w http.ResponseWriter, r *http.Request, pattern string) (authz.Decision, bool) {
	op, ok := controlOperations[pattern]
	if !ok {
		writeProblem(w, problemUnregistered, pattern+" is not a registered operation, so nothing can decide it")
		return authz.Decision{}, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxControlBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "the request could not be read: "+err.Error())
		return authz.Decision{}, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if s.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "there is no store to record the decision in, so nothing was done")
		return authz.Decision{}, false
	}
	sum := sha256.Sum256(body)
	// A signed-in person's role is weighed against the operation's; the local operator holds every role.
	var refused string
	if u, ok := userOf(r.Context()); ok {
		need, _ := authz.Lookup(op)
		if !need.Permits(u.Role) {
			refused = u.Called() + " holds the " + u.Role + " role, and " + op + " needs " + need.Role
		}
	}
	d, err := s.Store.Decide(r.Context(), authz.Request{Operation: op, Principal: principalOf(r),
		Subject: authz.RouteSubject(pattern), Allow: refused == "", Reason: refused,
		Evidence: controlEvidence{Route: pattern, Path: r.URL.Path, Query: r.URL.RawQuery, Host: r.Host,
			Body: hex.EncodeToString(sum[:])}})
	switch {
	case refused != "":
		writeProblem(w, problemRoleTooLow, refused)
		return d, false
	case err != nil:
		undecided(w, "the decision to act on this could not be recorded, so nothing was done", err)
		return d, false
	}
	return d, true
}

// principalOf is who asked: the person signed in, or without sign-in whoever is at this machine,
// or one reaching it remotely by the name they used.
func principalOf(r *http.Request) authz.Principal {
	if u, ok := userOf(r.Context()); ok {
		return u.Principal()
	}
	if isLoopback(r.Host) {
		return authz.Local("")
	}
	return authz.Remote(r.Host)
}

// undecided answers a decision that could not be recorded: for want of a key, or of the log.
func undecided(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, store.ErrNoDecisionKey) {
		writeProblem(w, problemNoDecisionKey, what+": "+store.ErrNoDecisionKey.Error())
		return
	}
	writeProblem(w, problemDecisionUnrecorded, what+": "+internalDetail(err))
}
