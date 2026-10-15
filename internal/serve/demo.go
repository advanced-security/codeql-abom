package serve

import (
	"net/http"
	"strings"
)

// Instance modes the interface distinguishes.
const (
	ModeNormal = "normal"
	ModeDemo   = "demo"
)

// Instance is what this server is, fixed at startup, for the interface to say so plainly. It names
// no path, address or credential.
type Instance struct {
	Mode     string `json:"mode"`
	Dataset  string `json:"dataset,omitempty"`
	Scenario string `json:"scenario,omitempty"`
	// Authoring is "copilot" when sessions run through Copilot, "unavailable" with the reason beside it.
	Authoring       string `json:"authoring,omitempty"`
	AuthoringReason string `json:"authoringReason,omitempty"`
	// Repositories is "simulated" in a Demo: fictional repositories served from loopback.
	Repositories string `json:"repositories,omitempty"`
}

// demoControls are the only state changes a Demo instance accepts: practicing the query lifecycle
// over its own synthetic data. Every other control, including one added later, is refused.
var demoControls = map[string]bool{
	"POST /api/query-requests":                    true,
	"PUT /api/query-requests/{id}":                true,
	"DELETE /api/query-requests/{id}":             true,
	"POST /api/query-requests/{id}/sessions":      true,
	"POST /api/query-requests/{id}/sessions/stop": true,
	"POST /api/runs/{id}/drafts":                  true,
	"POST /api/runs/{id}/drafts/stop":             true,
	"POST /api/queries/stage":                     true,
	"POST /api/queries/test":                      true,
	"POST /api/queries/ast":                       true,
	"POST /api/runs":                              true,
	"POST /api/runs/{id}/start":                   true,
	"POST /api/runs/{id}/cancel":                  true,
	"POST /api/runs/{id}/convert":                 true,
	"PUT /api/views/{name}":                       true,
	"DELETE /api/views/{name}":                    true,
	"PUT /api/discovery/suites/{tier}":            true,
}

// demoWithheld are reads that exist to carry data out of the instance.
var demoWithheld = map[string]bool{
	"GET /api/graph/export":            true,
	"GET /api/authoring-events/export": true,
}

func (s *Server) instance(w http.ResponseWriter, _ *http.Request) {
	out := s.Instance
	if out.Mode == "" {
		out.Mode = ModeNormal
	}
	writeJSON(w, out, nil)
}

// demoPolicy refuses, in a Demo instance, every control it does not name and every download.
func (s *Server) demoPolicy(routes *http.ServeMux, next http.Handler) http.Handler {
	if s.Instance.Mode != ModeDemo {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := routes.Handler(r)
		read := r.Method == http.MethodGet || r.Method == http.MethodHead
		switch {
		case read && (demoWithheld[pattern] || r.URL.Query().Has("download") ||
			strings.HasSuffix(r.URL.Path, "/closure.svg")):
			refuseInDemo(w, "This Demo instance does not export or download practice data.")
			return
		case !read && strings.Contains(pattern, "/api/") && !demoControls[pattern]:
			refuseInDemo(w, "This Demo instance does not offer this control: it practices on "+
				"synthetic data and changes nothing outside it.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func refuseInDemo(w http.ResponseWriter, message string) {
	w.Header().Set("X-Abom-Refused-By", "demo")
	writeError(w, http.StatusForbidden, message)
}

// allowedHost admits a name the operator listed exactly, such as the one a Codespace forwards.
func (s *Server) allowedHost(host string) bool {
	for _, h := range s.AllowedHosts {
		if h != "" && strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}
