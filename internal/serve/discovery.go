package serve

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/runner"
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
	// RequeueStale puts back what has not been analyzed at the identity the bound suites resolve
	// to now. It belongs to the loop rather than the store because resolving that identity is a
	// CodeQL call, and reading it from the store would report the corpus as current at exactly
	// the moment all of it went stale.
	RequeueStale(ctx context.Context) (expand.Selection, error)
	// RequeueRepository puts back the dependencies one repository answers, ahead of the ranking.
	RequeueRepository(name string) (expand.Selection, error)
}

// RequeueResult is what a selective requeue did, beside the state it left behind.
//
// Both in one answer, because either alone leaves the page misleading: the state cannot say what
// the button achieved, and the numbers alone leave the queue counts beside it a poll out of date.
type RequeueResult struct {
	Requeued  int `json:"requeued"`
	Expedited int `json:"expedited"`
	// CLIVersion and Packs say what staleness was judged against, so the count is checkable
	// rather than merely asserted.
	CLIVersion string `json:"cliVersion"`
	Packs      int    `json:"packs"`
	// NamesNoRepository counts unavailable work a retry left alone, because no access fixes a
	// reference that names no repository.
	NamesNoRepository int            `json:"namesNoRepository"`
	State             DiscoveryState `json:"state"`
}

// DiscoveryState is the whole picture the Discovery view renders from: what the loop is doing,
// what remains queued, and how far behind the derived numbers are.
type DiscoveryState struct {
	Available bool              `json:"available"`
	Status    supervisor.Status `json:"status"`
	Frontier  map[string]int    `json:"frontier"`
	// Tiers gives each dependency tier's size and how much of it is analyzed. Both are needed:
	// binding a suite costs by the tier's size, re-analyzing costs by what is on record, and a
	// tier being re-analyzed must not read as a tier holding nothing.
	Tiers   map[string]store.TierCount `json:"tiers"`
	Version store.GraphVersion         `json:"version"`
	// ResumeOnStart is whether a server start may act on a recorded intent to run. Off unless the
	// operator asked for it, so a restart never begins a crawl on its own.
	ResumeOnStart bool `json:"resumeOnStart"`
	// EverStarted is whether anybody has ever started discovery here. The loop's own state cannot
	// answer it: a supervisor is new in every process, so a restarted server reports `stopped` for
	// an installation that has crawled the whole organization, and "never started" and "stopped
	// for now" are opposite things to tell a reader.
	EverStarted bool `json:"everStarted"`
	// AdvisoriesFetchedAt is empty when advisories were never collected. Nothing here fetches
	// them, so their age belongs on screen next to everything derived from them.
	AdvisoriesFetchedAt string `json:"advisoriesFetchedAt"`
}

func (s *Server) discoveryStatus(w http.ResponseWriter, r *http.Request) {
	out, err := s.discoveryState()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, out, nil)
}

func (s *Server) discoveryState() (DiscoveryState, error) {
	out := DiscoveryState{Available: s.Discovery != nil, Frontier: map[string]int{}}
	if s.Discovery != nil {
		out.Status = s.Discovery.Status()
	}

	summary, err := s.Store.Summary()
	if err != nil {
		return DiscoveryState{}, err
	}
	out.AdvisoriesFetchedAt = summary.AdvisoriesFetchedAt

	counts, err := s.Store.FrontierCounts()
	if err != nil {
		return DiscoveryState{}, err
	}
	out.Frontier = counts

	tiers, err := s.Store.TierCounts()
	if err != nil {
		return DiscoveryState{}, err
	}
	out.Tiers = tiers

	version, err := s.Store.GraphVersion()
	if err != nil {
		return DiscoveryState{}, err
	}
	out.Version = version

	resume, err := s.Store.DiscoveryResumeOnStart()
	if err != nil {
		return DiscoveryState{}, err
	}
	out.ResumeOnStart = resume

	_, known, err := s.Store.DiscoveryIntent()
	if err != nil {
		return DiscoveryState{}, err
	}
	out.EverStarted = known
	return out, nil
}

// discoveryResumeOnStart records whether a restart may resume a crawl that was running.
//
// Guarded like the other controls: it decides whether a future start spends rate limit, disk and
// every core on the machine without anybody present to have asked.
func (s *Server) discoveryResumeOnStart(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	var body struct {
		Resume bool `json:"resume"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "a resume-on-start request says whether to resume")
		return
	}
	if err := s.Store.SetDiscoveryResumeOnStart(body.Resume); err != nil {
		writeJSON(w, nil, err)
		return
	}
	s.discoveryStatus(w, r)
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

// suiteBinding is what a control needs to offer the choice: everything that could be bound, and
// what each tier is bound to now.
type suiteBinding struct {
	Available []string            `json:"available"`
	Tiers     map[string][]string `json:"tiers"`
	// Counts is each tier's size and how much of it is analyzed, so the cost of changing what it
	// runs is on screen before it is spent rather than discovered during the next crawl.
	Counts map[string]store.TierCount `json:"counts"`
	// Contents is what each suite last resolved to. A suite nobody has resolved is absent, not
	// present with a zero count: the interface has to be able to say it does not know yet.
	Contents map[string]suiteDetail `json:"contents,omitempty"`
	// Verified reports whether any identity has been established. Without one, nothing has shown
	// that the operator owns any dependency, so an owned count of zero would be a claim rather
	// than a measurement and the interface draws no column for it.
	Verified bool `json:"verified"`
}

// suiteDetail is the level between a suite and its queries: the packs behind it, each with the
// version that changes when the queries do.
type suiteDetail struct {
	Packs      []packDetail `json:"packs"`
	QueryCount int          `json:"queryCount"`
	ResolvedAt string       `json:"resolvedAt"`
}

type packDetail struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Queries []string `json:"queries"`
}

// suiteDetails groups what each suite resolved to by the pack that supplied each query.
func suiteDetails(contents map[string][]store.SuiteQuery) map[string]suiteDetail {
	out := map[string]suiteDetail{}
	for suite, queries := range contents {
		byPack := map[string]*packDetail{}
		var order []string
		detail := suiteDetail{QueryCount: len(queries)}
		for _, q := range queries {
			if q.ResolvedAt > detail.ResolvedAt {
				detail.ResolvedAt = q.ResolvedAt
			}
			p, ok := byPack[q.Pack]
			if !ok {
				p = &packDetail{Name: q.Pack, Version: q.PackVersion}
				byPack[q.Pack] = p
				order = append(order, q.Pack)
			}
			p.Queries = append(p.Queries, q.QueryID)
		}
		sort.Strings(order)
		for _, name := range order {
			detail.Packs = append(detail.Packs, *byPack[name])
		}
		out[suite] = detail
	}
	return out
}

// tierSuites reports what each dependency tier runs, and what it could run.
func (s *Server) tierSuites(w http.ResponseWriter, _ *http.Request) {
	bound, err := s.Store.TierSuites()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	counts, err := s.Store.TierCounts()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	available, err := runner.BindableSuites()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	// Anything already bound stays offered even if it is not on disk here: a binding may name a
	// suite from an installed pack, and dropping it from the list would make it look invented.
	seen := map[string]bool{}
	for _, suite := range available {
		seen[suite] = true
	}
	for _, suites := range bound {
		for _, suite := range suites {
			if !seen[suite] {
				seen[suite] = true
				available = append(available, suite)
			}
		}
	}
	sort.Strings(available)
	contents, err := s.Store.SuiteContents()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	verified, err := s.Store.Verified()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, suiteBinding{
		Available: available, Tiers: bound, Counts: counts,
		Contents: suiteDetails(contents), Verified: verified,
	}, nil)
}

// setTierSuites rewrites what one tier runs.
//
// Guarded like the other controls that spend the machine's time: what a tier binds to decides
// how much CodeQL every future round of that tier pays for.
func (s *Server) setTierSuites(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	var suites []string
	if err := json.NewDecoder(r.Body).Decode(&suites); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON array of suite paths")
		return
	}
	if err := s.Store.SetTierSuites(r.PathValue("tier"), suites); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.tierSuites(w, r)
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

// discoveryRequeueStale puts back only what is out of date against the current pack identity.
//
// The tier buttons beside it re-clone everything in the tier; this re-clones what actually
// moved. Both spend the machine's time, so both are guarded the same way.
func (s *Server) discoveryRequeueStale(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	if s.Discovery == nil {
		writeError(w, http.StatusServiceUnavailable, "discovery is not available on this server")
		return
	}
	got, err := s.Discovery.RequeueStale(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeRequeued(w, got)
}

// discoveryRequeueRepository puts back the dependencies one repository answers.
//
// The name is in the body rather than the path: an owner and a name are two segments, and a
// two-segment route would be indistinguishable from the tier route beside it.
func (s *Server) discoveryRequeueRepository(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	if s.Discovery == nil {
		writeError(w, http.StatusServiceUnavailable, "discovery is not available on this server")
		return
	}
	var body struct {
		Repository string `json:"repository"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON object naming a repository")
		return
	}
	// An empty name matches nothing, and reporting success for it leaves an operator watching a
	// crawl that was never going to run.
	if strings.TrimSpace(body.Repository) == "" {
		writeError(w, http.StatusBadRequest, "name a repository as owner/name")
		return
	}
	got, err := s.Discovery.RequeueRepository(body.Repository)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeRequeued(w, got)
}

// discoveryRetryUnavailable puts back every dependency that could not be fetched.
//
// Only on request: Git answers a missing credential and a missing repository alike, so retrying
// on a schedule would re-clone what is gone every cycle. The operator knows when access changed.
func (s *Server) discoveryRetryUnavailable(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	got, err := s.Store.RetryUnavailable()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if s.Discovery != nil && got.Requeued > 0 {
		s.Discovery.Wake()
	}
	state, err := s.discoveryState()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, RequeueResult{
		Requeued: got.Requeued, NamesNoRepository: got.NamesNoRepository, State: state,
	}, nil)
}

func (s *Server) writeRequeued(w http.ResponseWriter, got expand.Selection) {
	// Expediting alone is worth waking for: the loop would otherwise sleep through a request to
	// run something next.
	if got.Requeued > 0 || got.Expedited > 0 {
		s.Discovery.Wake()
	}
	state, err := s.discoveryState()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, RequeueResult{
		Requeued: got.Requeued, Expedited: got.Expedited,
		CLIVersion: got.CLIVersion, Packs: got.Packs, State: state,
	}, nil)
}

// controlHeader is required on every control request.
//
// A browser cannot set it cross-origin without a preflight, which turns a silent cross-site
// form post into a request this server never sees.
const controlHeader = "X-Abom-Control"

func (s *Server) allowHost(w http.ResponseWriter, r *http.Request) bool {
	if !s.AllowRemoteControl && !isLoopback(r.Host) {
		writeError(w, http.StatusForbidden,
			"ABOM is accessible only over a loopback address; "+
				"restart with --allow-remote-control to change that")
		return false
	}
	return true
}

// allowControl protects state changes and operations using the operator's credentials.
func (s *Server) allowControl(w http.ResponseWriter, r *http.Request) bool {
	if !s.allowHost(w, r) {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		writeError(w, http.StatusForbidden, "cross-site requests cannot control ABOM")
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
