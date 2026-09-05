package serve

import (
	"net"
	"net/http"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// Discovery is the loop the interface observes and controls. Nil leaves the endpoints in place,
// answering that discovery is not available here rather than 404ing a route the client expects.
type Discovery interface {
	Status() supervisor.Status
	Start() *supervisor.Problem
	Pause()
	// Wake asks a running loop to look for work now rather than at the end of its interval.
	Wake()
}

// DiscoveryState is the whole picture the Discovery view renders from: what the loop is doing,
// what remains queued, and how far behind the derived numbers are.
type DiscoveryState struct {
	Available bool              `json:"available"`
	Status    supervisor.Status `json:"status"`
	Frontier  map[string]int    `json:"frontier"`
	// Tiers counts analyzed work per dependency tier, so a control that re-analyzes one can say
	// what it will cost before it is pressed.
	Tiers   map[string]int     `json:"tiers"`
	Version store.GraphVersion `json:"version"`
	// AdvisoriesFetchedAt is empty when advisories were never collected. Nothing here fetches
	// them, so their age belongs on screen next to everything derived from them.
	AdvisoriesFetchedAt string `json:"advisoriesFetchedAt"`
}

func (s *Server) discoveryStatus(w http.ResponseWriter, r *http.Request) {
	out := DiscoveryState{Available: s.Discovery != nil, Frontier: map[string]int{}}
	if s.Discovery != nil {
		out.Status = s.Discovery.Status()
	}

	summary, err := s.Store.Summary()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out.AdvisoriesFetchedAt = summary.AdvisoriesFetchedAt

	counts, err := s.Store.FrontierCounts()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out.Frontier = counts

	tiers, err := s.Store.TierCounts()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out.Tiers = tiers

	version, err := s.Store.GraphVersion()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out.Version = version
	writeJSON(w, out, nil)
}

func (s *Server) discoveryStart(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	if s.Discovery == nil {
		writeError(w, http.StatusServiceUnavailable, "discovery is not available on this server")
		return
	}
	if problem := s.Discovery.Start(); problem != nil {
		writeJSON(w, problem, nil)
		return
	}
	s.discoveryStatus(w, r)
}

func (s *Server) discoveryPause(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	if s.Discovery == nil {
		writeError(w, http.StatusServiceUnavailable, "discovery is not available on this server")
		return
	}
	s.Discovery.Pause()
	s.discoveryStatus(w, r)
}

// discoveryRequeue puts a whole tier back in the queue.
//
// Guarded like Start, and for the same reason: it spends the machine's time on clones and
// database builds. It is also the one control that can undo nothing — what is already known
// stays until a re-run replaces it.
func (s *Server) discoveryRequeue(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	n, err := s.Store.RequeueTier(r.PathValue("tier"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Nothing is claimed until the loop looks again, so a paused loop leaves the work waiting
	// rather than silently starting it.
	if s.Discovery != nil && n > 0 {
		s.Discovery.Wake()
	}
	s.discoveryStatus(w, r)
}

// controlHeader is required on every control request.
//
// A browser cannot set it cross-origin without a preflight, which turns a silent cross-site
// form post into a request this server never sees.
const controlHeader = "X-Abom-Control"

// allowControl guards the endpoints that start subprocesses.
//
// Starting discovery makes this process clone repositories and run CodeQL, so a page on another
// origin must not be able to trigger it, and neither must a host that was never meant to reach
// it. Read endpoints are deliberately not guarded: they expose the same data the interface
// already shows.
func (s *Server) allowControl(w http.ResponseWriter, r *http.Request) bool {
	if !s.AllowRemoteControl && !isLoopback(r.Host) {
		writeError(w, http.StatusForbidden,
			"discovery is controlled only over a loopback address; "+
				"restart with --allow-remote-control to change that")
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		writeError(w, http.StatusForbidden, "cross-site requests cannot control discovery")
		return false
	}
	if r.Header.Get(controlHeader) == "" {
		writeError(w, http.StatusForbidden, "missing "+controlHeader)
		return false
	}
	return true
}

// isLoopback reports whether a request arrived addressed to this machine.
//
// The Host header is what a rebinding attack controls, so it is checked rather than the socket:
// a name that resolves elsewhere must not be treated as local just because the connection is.
func isLoopback(host string) bool {
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.Trim(name, "[]")
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}
