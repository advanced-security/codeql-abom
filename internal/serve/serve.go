// Package serve exposes the ABOM over HTTP for the user interface.
//
// Queries stay in Go against the full database rather than being shipped to the browser: the
// corpus reaches gigabytes at organization scale, which no client can hold, and every read the
// interface needs already exists here.
package serve

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/pprof"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/enumerate"
	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"

	// Aliased because this package already has a `view` of its own, for accounts.
	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// Server answers interface queries from an ABOM database.
type Server struct {
	Store *store.Store
	// UI serves the interface assets. Nil disables it, leaving only the API.
	UI fs.FS
	// Verify establishes which accounts are the operator's. Nil asks the gh CLI.
	Verify func(context.Context) (ghauth.Identity, error)
	// Discovery is the expansion loop. Nil answers that discovery is unavailable here.
	Discovery Discovery
	// Enumerate reads one declared account, bounded by what a previous read established. Nil
	// answers that this build cannot enumerate, which is not the same answer as an account
	// holding nothing.
	Enumerate enumerate.Read
	// EnumerateSchema reads what an account declares its custom properties to be. Nil leaves the
	// organization's defaults unknown, so no bucket is labeled as the one nobody chose.
	EnumerateSchema enumerate.ReadSchema
	// FullListingEvery is how often every page of an account is read rather than a prefix of it.
	// Zero takes enumerate.DefaultFullListingEvery.
	FullListingEvery time.Duration
	// GroupProperty names the custom property a screen opens on when grouping repositories. Empty
	// means the operator named none, and nothing is guessed in its place. It is configuration and
	// never a term of a predicate: what a filter means travels in the filter.
	GroupProperty string
	// QueueOwned puts the operator's own repositories in the analysis queue. Nil leaves them
	// recorded and never analyzed, which is the state enumeration exists to end.
	QueueOwned func() (int, error)
	// SnapshotPreview reports what a submission would send. Nil answers that this build cannot
	SnapshotPreview func(repository string) (SnapshotPreview, error)
	// SnapshotSubmit writes a snapshot to GitHub. Nil leaves the endpoint unavailable, which is
	// the right default: the write cannot be withdrawn and there is no delete endpoint.
	SnapshotSubmit func(repository string) (SnapshotResult, error)
	// ApprovedTargets names the repositories that may receive a snapshot. Renewal iterates this
	// and nothing else, so a repository the operator never approved cannot be written to by a
	// batch pass that was aimed at something else.
	ApprovedTargets func() []string
	// AllowRemoteControl lets a non-loopback client start discovery. Off by default because
	// starting it makes this process clone repositories and run CodeQL.
	AllowRemoteControl bool
	// Log receives failed requests. Nil discards them, which leaves a 500 visible only to the
	// client that hit it.
	Log io.Writer
	// Diagnostics exposes /api/metrics and /debug/pprof/. Off by default: route names with their
	// latencies are a map of what exists and what is expensive, and pprof serves goroutine stacks
	// and heap contents. One flag for both, because two invites the answer "metrics are the safe
	// half" and the safe half is the one left running.
	Diagnostics bool

	// WorkDir is where checkouts are fetched, so a prune can take the working copies of what it
	// removed. Empty means nothing is known about the disk, which is not the same as nothing
	// being stale on it.
	WorkDir string

	// metrics is built by Handler, so a zero Server stays usable and every request is counted
	// whether or not Log is set.
	metrics *metrics

	// stale is the last answer to what no longer belongs, written by a check that runs off the
	// request path. Guarded because startup writes it from a goroutine while requests read it.
	staleMu sync.RWMutex
	stale   Staleness
}

// Handler builds the routing tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.metrics == nil {
		s.metrics = newMetrics()
	}

	mux.HandleFunc("GET /api/summary", s.summary)
	mux.HandleFunc("GET /api/remediation", reading(s.remediation,
		"top", "offset", "sort", "desc", "view", "properties", "notProperties"))
	mux.HandleFunc("GET /api/advisories", reading(s.advisories, "top", "properties", "notProperties"))
	mux.HandleFunc("GET /api/exposure", reading(s.exposure, "by", "top", "properties", "notProperties"))
	mux.HandleFunc("GET /api/graph", reading(s.graph,
		"focus", "hops", "max", "direction", "granularity", "expand", "mode"))
	mux.HandleFunc("GET /api/graph/full", s.fullGraph)
	mux.HandleFunc("GET /api/graph/export", s.graphExport)
	mux.HandleFunc("GET /api/graph/layout", s.graphLayout)
	mux.HandleFunc("GET /api/graph/version", s.graphVersion)
	mux.HandleFunc("GET /api/graph/delta", s.graphDelta)
	mux.HandleFunc("GET /api/bom", s.bom)
	mux.HandleFunc("GET /api/discovery/status", s.discoveryStatus)
	mux.HandleFunc("POST /api/discovery/start", s.discoveryStart)
	mux.HandleFunc("POST /api/discovery/pause", s.discoveryPause)
	mux.HandleFunc("POST /api/discovery/requeue/{tier}", s.discoveryRequeue)
	mux.HandleFunc("POST /api/discovery/requeue-stale", s.discoveryRequeueStale)
	mux.HandleFunc("POST /api/discovery/requeue-repository", s.discoveryRequeueRepository)
	mux.HandleFunc("GET /api/discovery/suites", s.tierSuites)
	mux.HandleFunc("GET /api/discovery/objective", s.objective)
	mux.HandleFunc("PUT /api/discovery/suites/{tier}", s.setTierSuites)
	mux.HandleFunc("GET /api/nodes", reading(s.nodes, "q", "top", "properties", "notProperties"))
	mux.HandleFunc("GET /api/node/{key...}", s.node)
	mux.HandleFunc("GET /api/pinning", reading(s.pinning,
		"scope", "parent", "q", "style", "flagged", "top", "properties", "notProperties"))
	mux.HandleFunc("GET /api/pinning/totals", reading(s.pinningTotals,
		"q", "style", "flagged", "properties", "notProperties"))
	mux.HandleFunc("GET /api/accounts", s.accounts)
	mux.HandleFunc("GET /api/owned-scope", s.ownedScope)
	mux.HandleFunc("GET /api/properties", reading(s.properties, "property"))
	mux.HandleFunc("GET /api/policies", s.policies)
	mux.HandleFunc("PUT /api/policies", s.savePolicy)
	mux.HandleFunc("POST /api/policies/{name}/simulate", s.simulatePolicy)
	mux.HandleFunc("POST /api/policies/{name}/adopt", s.adoptPolicy)
	mux.HandleFunc("POST /api/policies/{name}/waivers", s.grantWaiver)
	mux.HandleFunc("GET /api/policy-waivers", s.policyWaivers)
	mux.HandleFunc("GET /api/match", s.match)
	mux.HandleFunc("GET /api/views", s.views)
	mux.HandleFunc("PUT /api/views/{name}", s.saveView)
	mux.HandleFunc("DELETE /api/views/{name}", s.deleteView)
	mux.HandleFunc("PUT /api/owned-scope", s.setOwnedScope)
	mux.HandleFunc("POST /api/owned-scope/preview", s.previewOwnedScope)
	mux.HandleFunc("POST /api/owned-scope/enumerate", s.enumerate)
	mux.HandleFunc("GET /api/prune", s.prune)
	mux.HandleFunc("GET /api/prune/staleness", s.staleness)
	mux.HandleFunc("POST /api/prune/apply", s.pruneApply)
	mux.HandleFunc("POST /api/prune/compact", s.pruneCompact)
	mux.HandleFunc("POST /api/accounts/refresh", s.refreshAccounts)
	mux.HandleFunc("GET /api/owner-candidates", s.ownerCandidates)
	mux.HandleFunc("GET /api/frontier", reading(s.frontier, "status", "top"))
	mux.HandleFunc("GET /api/actions", reading(s.actions,
		"q", "top", "offset", "sort", "desc", "view",
		"kinds", "origins", "minReach", "scope", "flags", "ref",
		"rules", "notRules", "provider", "severity", "repositories", "mode",
		"properties", "notProperties"))
	mux.HandleFunc("GET /api/workflows", reading(s.workflows,
		"q", "top", "offset", "sort", "desc", "view",
		"scope", "flags", "properties", "notProperties"))
	mux.HandleFunc("GET /api/jobs", reading(s.jobs,
		"q", "top", "offset", "sort", "desc", "view",
		"scope", "flags", "properties", "notProperties"))
	mux.HandleFunc("GET /api/relationships", reading(s.relationships,
		"q", "top", "offset", "sort", "desc", "view",
		"scope", "boundary", "edgeKinds", "ref", "rules", "notRules", "mode",
		"properties", "notProperties"))
	mux.HandleFunc("GET /api/repositories",
		reading(s.repositories, "q", "top", "offset", "sort", "desc", "view",
			"scope", "properties", "notProperties"))
	mux.HandleFunc("GET /api/repositories/{owner}/{name}", s.repository)
	mux.HandleFunc("POST /api/repositories/{owner}/{name}/snapshot/preview", s.snapshotPreview)
	mux.HandleFunc("POST /api/repositories/{owner}/{name}/snapshot/submit", s.snapshotSubmit)
	mux.HandleFunc("GET /api/submissions", s.submissions)
	mux.HandleFunc("POST /api/submissions/renew", s.renew)

	if s.UI != nil {
		// Registered for every method rather than GET alone: a method-restricted catch-all
		// cannot coexist with the /api/ pattern below, which ServeMux rejects outright.
		mux.Handle("/", s.ui())
	}
	if s.Diagnostics {
		s.diagnostics(mux)
	}
	// Anything under /api/ that no route claimed is absent, whatever the method. Without this a
	// withdrawn route answers a PUT with 405, which says the resource is still there.
	mux.HandleFunc("/api/", http.NotFound)
	return gzipped(s.observe(mux))
}

// diagnostics registers the surfaces an operator has to ask for by name.
func (s *Server) diagnostics(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/metrics", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, s.metrics.report(), nil)
	})
	// The whole /debug/pprof/ tree, not only the index: the profiles are separate handlers.
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
}

// observe records what every request cost, and logs the ones worth a line.
//
// Recording is unconditional. The duration was already being computed here and thrown away unless
// the request failed or took over slowRequest, which is two seconds — so an endpoint answering in
// 0.68 s left no trace at all, and that is the gap this exists to close.
//
// Handlers answer an error to the caller and nothing else, so a query that breaks for every
// reader leaves no trace on the machine actually running. Successful requests are not logged:
// the interface polls continuously, and a line each would bury the failures.
func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No handler here composes markup, so a browser that re-reads a body as markup can only
		// be wrong. Set before the handler runs: a header added after WriteHeader never leaves,
		// and the error path writes its status first.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		rec := &recorder{ResponseWriter: w, status: http.StatusOK, marshalNanos: -1}
		start := time.Now()
		next.ServeHTTP(rec, r)
		took := time.Since(start)

		marshalMillis := int64(-1)
		if rec.marshalNanos >= 0 {
			marshalMillis = rec.marshalNanos / int64(time.Millisecond)
		}
		// ServeMux sets Pattern on this request while matching, so the route is available here
		// without parsing the URL — which is what keeps the key bounded as the corpus grows.
		s.metrics.record(r.Pattern, rec.status, took.Milliseconds(), rec.written, marshalMillis)

		if s.Log == nil {
			return
		}
		switch {
		case rec.status >= http.StatusBadRequest:
			fmt.Fprintf(s.Log, "%s serve: %d %s %s %s\n", time.Now().UTC().Format(time.RFC3339),
				rec.status, r.Method, r.URL.RequestURI(), strings.TrimSpace(rec.body.String()))
		case took >= slowRequest:
			// A graph that answers correctly but takes seconds is the complaint users actually
			// report, and it is invisible in a log that only keeps failures.
			fmt.Fprintf(s.Log, "%s serve: slow %s %s took %dms\n",
				time.Now().UTC().Format(time.RFC3339), r.Method, r.URL.RequestURI(),
				took.Milliseconds())
		}
	})
}

// slowRequest is the point past which a reader notices waiting.
const slowRequest = 2 * time.Second

// recorder keeps the status, the bytes written, the time spent encoding, and for a failure the
// body that explains it.
type recorder struct {
	http.ResponseWriter
	status  int
	written int64
	// marshalNanos stays negative until something encodes, so a handler that never marshaled is
	// distinguishable from one that marshaled instantly.
	marshalNanos int64
	body         bytes.Buffer
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	// Name the body before the first byte leaves. net/http types an undeclared response by
	// looking at it, so a document that merely opens like markup is served as markup. Every
	// handler names its own; this is the last place that knows one did not.
	if header := r.Header(); header.Get("Content-Type") == "" {
		header.Set("Content-Type", "text/plain; charset=utf-8")
	}
	// Only a failure's body is kept; a successful response is megabytes of graph.
	if r.status >= http.StatusBadRequest && r.body.Len() < 2048 {
		r.body.Write(b)
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)
	return n, err
}

// assetRoots hold real files. A miss under one of them is a broken build, not a client route.
var assetRoots = []string{"_framework/", "_content/", "css/", "js/", "lib/"}

// ui serves the interface, falling back to its entry point so client-side routes survive a
// reload.
//
// A missing asset must 404 rather than receive HTML, or the browser reports it as a syntax error
// in whatever it thought it was loading, which says nothing about the actual problem. That
// cannot be decided by looking for a dot: node keys end in things like `ci.yml`, so a route such
// as /node/owner/repo/.github/workflows/ci.yml is indistinguishable from a file by name alone.
// Where the path lives is the reliable signal.
func (s *Server) ui() http.Handler {
	files := http.FileServer(http.FS(s.UI))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(s.UI, name); err != nil {
			for _, root := range assetRoots {
				if strings.HasPrefix(name, root) {
					http.NotFound(w, r)
					return
				}
			}
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		// Revalidate rather than serve from cache. A browser holding an old ES module after a
		// rebuild fails in a way that looks like a code bug and costs far more than the
		// conditional request does; no-cache still allows a 304.
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	sum, err := s.Store.Summary()
	writeJSON(w, sum, err)
}

// remediation lists one page of the advisories unit, worst first across the whole match.
//
// The page carries how many live advisories reach nothing under review, because that exclusion is
// correct and invisible: a coverage strip counting 11 over a table holding 3 taught a reader that
// eight had gone somewhere, without saying where.
func (s *Server) remediation(w http.ResponseWriter, r *http.Request) {
	spec, err := s.graphPredicate(r, "top", "offset", "sort", "desc")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsAdvisories(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, unlistable(refused))
		return
	}
	page, err := s.Store.AdvisoriesPage(store.AdvisoriesQuery{
		Offset:   intParam(r, "offset", 0),
		Limit:    intParam(r, "top", 50),
		Grouping: scopeFromSpec(spec),
	})
	writeJSON(w, page, err)
}

func (s *Server) advisories(w http.ResponseWriter, r *http.Request) {
	scope, err := repositoryScope(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.Store.AdvisoriesByReach(intParam(r, "top", 50), scope)
	writeJSON(w, items, err)
}

func (s *Server) exposure(w http.ResponseWriter, r *http.Request) {
	scope, err := repositoryScope(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch r.URL.Query().Get("by") {
	case "repository", "repo":
		items, err := s.Store.ExposuresByRepository(intParam(r, "top", 200), scope)
		writeJSON(w, items, err)
	default:
		items, err := s.Store.ExposureByAdvisory(scope)
		writeJSON(w, items, err)
	}
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	focus := q.Get("focus")
	if focus == "" {
		writeError(w, http.StatusBadRequest, "focus is required")
		return
	}
	spec, err := s.graphPredicate(r, "focus", "hops", "max", "direction", "granularity", "expand")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	g, err := s.Store.Subgraph(store.SubgraphOptions{
		Focus:       focus,
		Hops:        intParam(r, "hops", 1),
		MaxNodes:    intParam(r, "max", store.DefaultSubgraphNodes),
		Direction:   q.Get("direction"),
		Granularity: q.Get("granularity"),
		Expand:      q.Get("expand") == "true",
		Mode:        spec.Mode,
	})
	writeJSON(w, g, err)
}

// fullGraph ships the entire corpus in one payload.
//
// The interface filters this in the browser, so the cost is paid once on arrival rather than on
// every interaction. Steps are folded into their workflow unless asked for: 3.6k nodes against
// 23k, for the same set of relationships.
func (s *Server) fullGraph(w http.ResponseWriter, r *http.Request) {
	spec, err := s.graphPredicate(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsGraph(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, undrawable(refused))
		return
	}
	opts, err := s.graphOptions(spec)
	if err != nil {
		writeJSON(w, store.Graph{}, err)
		return
	}
	g, err := s.Store.FullGraph(opts)
	g.ClientApplied = spec.ClientApplied()
	writeJSON(w, g, err)
}

// undrawable is the wording for a term the graph neither applies nor hands to whoever draws it.
func undrawable(terms []string) string {
	return strings.Join(terms, ", ") +
		" is applied by nothing here; a term is enforced or refused, never dropped"
}

// graphPredicate reads the shared predicate from a request, ignoring the named parameters that are
// cursors rather than filters.
//
// Parsed through the shared predicate rather than field by field, so an unknown filter is refused
// here instead of silently matching everything. The browser and the pull request gate read the
// same text, and a typo that widened the match would be invisible in all three.
//
// `?view=<name>` is resolved first and supplies the whole predicate, because a name is a reference
// to a question rather than another clause of one.
func (s *Server) graphPredicate(r *http.Request, cursors ...string) (viewspec.Spec, error) {
	if spec, named, err := s.resolveView(r, cursors...); named {
		return spec, err
	}
	q := r.URL.Query()
	for _, key := range cursors {
		q.Del(key)
	}
	return viewspec.Parse(q.Encode())
}

// graphOptions turns a predicate into the options both graph endpoints answer from.
//
// Shared so the delta cannot drift into answering a different question from the payload it is
// updating: it carried arrivals from outside the declared scope until 2026-09-10, because it built
// its options from `steps` alone.
//
// `kinds`, `search`, the finding predicates and the neighborhood walk are deliberately absent.
// Measured 2026-09-10, they sever 74% to 99% of their own match's edges, because the graph is
// bipartite by kind — applying them here would ship a scatter instead of a graph. They belong to
// the set-answering endpoint the gate needs, where structure is irrelevant.
//
// `properties` is the exception, and the reason it is not one of them: it names repositories
// rather than findings, so it narrows to a set of sources and then keeps everything they reach.
// That is a subgraph rather than a scatter, and it is the same mechanism `scope=owned` uses.
func (s *Server) graphOptions(spec viewspec.Spec) (store.FullGraphOptions, error) {
	opts := store.FullGraphOptions{
		IncludeSteps:   spec.Steps,
		Scope:          spec.Scope,
		Boundary:       spec.Boundary,
		EdgeKinds:      spec.EdgeKinds,
		Origins:        spec.Origins,
		MinReach:       spec.MinReach,
		RefStyle:       spec.RefStyle,
		Mode:           spec.Mode,
		UnanalyzedOnly: slices.Contains(spec.Flags, viewspec.FlagUnanalyzed),
		// Configuration rather than a term of the question: it labels nodes so the browser can
		// group by them, and labeling is not narrowing.
		GroupProperty: s.GroupProperty,
	}
	// The same KeepList pruning acts on, so what the operator is shown and what removal would
	// keep are one answer rather than two that can drift apart.
	if opts.Scope == store.ScopeOwned {
		keep, _, err := (&graph.Builder{Store: s.Store}).KeepList()
		if err != nil {
			return store.FullGraphOptions{}, err
		}
		opts.Closure = keep
	}
	if len(spec.Properties) > 0 || len(spec.NotProperties) > 0 {
		// Recorded separately from the list, so a filter matching no repository draws nothing
		// rather than everything.
		opts.GroupFiltered = true
		keep, err := (&graph.Builder{Store: s.Store}).
			GroupingKeepList(spec.Properties, spec.NotProperties)
		if err != nil {
			return store.FullGraphOptions{}, err
		}
		opts.GroupClosure = keep
	}
	return opts, nil
}

func (s *Server) graphVersion(w http.ResponseWriter, r *http.Request) {
	version, err := s.Store.GraphVersion()
	writeJSON(w, version, err)
}

func (s *Server) graphDelta(w http.ResponseWriter, r *http.Request) {
	// `since` is a cursor, not a filter: it says where the reader got to, not what they asked for.
	spec, err := s.graphPredicate(r, "since")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsGraph(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, undrawable(refused))
		return
	}
	opts, err := s.graphOptions(spec)
	if err != nil {
		writeJSON(w, store.GraphDelta{}, err)
		return
	}
	delta, err := s.Store.GraphDelta(int64(intParam(r, "since", 0)), opts)
	if err == nil && s.metrics != nil {
		s.metrics.recordDelta(delta.Reset)
	}
	writeJSON(w, delta, err)
}

func (s *Server) nodes(w http.ResponseWriter, r *http.Request) {
	scope, err := repositoryScope(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.Store.SearchNodes(r.URL.Query().Get("q"), intParam(r, "top", 50), scope)
	writeJSON(w, items, err)
}

// node answers for one key, which may itself contain slashes: an action is `owner/name` and a
// workflow is a repository-qualified path.
func (s *Server) node(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	// The impact of changing a node hangs off the same key, so it shares the route rather than
	// needing a second one that could disagree about what the key is.
	if rest, ok := strings.CutSuffix(key, "/-/impact"); ok {
		impact, err := s.Store.BumpImpactOf(rest)
		writeJSON(w, impact, err)
		return
	}
	// Which repositories under review actually reach this, and by what route. Same key, same
	// route, for the same reason.
	if rest, ok := strings.CutSuffix(key, "/-/reach"); ok {
		reach, err := (&graph.Builder{Store: s.Store}).ReachOfNode(rest, 0)
		writeJSON(w, reach, err)
		return
	}
	// Everything any query has said about this node, each answer naming the query that made it.
	if rest, ok := strings.CutSuffix(key, "/-/observations"); ok {
		obs, err := s.Store.ObservationsOf(store.SubjectNode, rest)
		writeJSON(w, obs, err)
		return
	}
	// Every repository naming one version, which the Discovery grid can only preview. The ref is
	// a parameter rather than part of the path: it may be empty, and an empty path segment would
	// make "default branch" indistinguishable from a malformed route.
	if rest, ok := strings.CutSuffix(key, "/-/users"); ok {
		users, err := s.Store.NodeUsersOf(rest, r.URL.Query().Get("ref"))
		writeJSON(w, users, err)
		return
	}

	detail, err := s.Store.NodeDetailOf(key)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, detail, nil)
}

func (s *Server) pinning(w http.ResponseWriter, r *http.Request) {
	f, err := pinningFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.Store.PinningLevel(r.URL.Query().Get("scope"), r.URL.Query().Get("parent"),
		f, intParam(r, "top", 1000))
	writeJSON(w, rows, err)
}

func (s *Server) pinningTotals(w http.ResponseWriter, r *http.Request) {
	f, err := pinningFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	total, err := s.Store.PinningTotals(f)
	writeJSON(w, total, err)
}

// accountsView is the identity as the interface needs to read it: who was verified, what that
// established, and — when it could not be established — what the operator should do about it.
type accountsView struct {
	Authenticated bool            `json:"authenticated"`
	Login         string          `json:"login"`
	Host          string          `json:"host"`
	VerifiedAt    *time.Time      `json:"verifiedAt"`
	Accounts      []store.Account `json:"accounts"`
	Problem       *ghauth.Problem `json:"problem"`
	// CanReadMemberships is why a declaration is only unproven rather than refuted. Without it a
	// blind token looks exactly like `gh` never having answered, and the remedy offered is to ask
	// again — a loop that cannot terminate.
	CanReadMemberships bool `json:"canReadMemberships"`
}

func view(v store.Verification, problem *ghauth.Problem) accountsView {
	out := accountsView{
		Authenticated:      v.Login != "",
		Login:              v.Login,
		Host:               v.Host,
		Accounts:           v.Accounts,
		Problem:            problem,
		CanReadMemberships: v.CanReadMemberships(),
	}
	if out.Accounts == nil {
		out.Accounts = []store.Account{}
	}
	if !v.VerifiedAt.IsZero() {
		out.VerifiedAt = &v.VerifiedAt
	}
	return out
}

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	stored, err := s.Store.Accounts()
	writeJSON(w, view(stored, nil), err)
}

// declaredAccountView is a declaration with what `gh` has to say about it.
//
// The state is computed here rather than in the browser because it decides whether an ownership
// mark may be drawn, and two derivations of that would be two answers to who owns what.
type declaredAccountView struct {
	store.DeclaredAccount
	State string `json:"state"`
	// Enumeration is when this account was last read and by which route. A repository deleted
	// after the last full listing is still reported until the next one, which is only defensible
	// if a reader can see how long ago that was.
	Enumeration store.EnumerationMarks `json:"enumeration"`
}

// ownedScope serves what the operator has declared is theirs, which is what discovery is bounded
// by and what every "yours" figure is counted against.
func (s *Server) ownedScope(w http.ResponseWriter, r *http.Request) {
	declared, err := s.Store.OwnedScope()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	// A failure to read the verification leaves every state unknown, which is the honest answer:
	// it is exactly the case where nothing has been established either way.
	verified, _ := s.Store.Accounts()

	out := make([]declaredAccountView, 0, len(declared))
	for _, a := range declared {
		// Marks that cannot be read leave the account unread rather than undeclared: what the
		// operator said is theirs does not depend on whether anything has looked at it yet.
		marks, _, _ := s.Store.EnumerationState(a.Owner)
		out = append(out, declaredAccountView{a, store.DeclarationState(a, verified), marks})
	}
	writeJSON(w, out, nil)
}

// setOwnedScope replaces the declaration. An empty list is a real instruction — it stops
// discovery — so it is accepted rather than treated as a malformed body.
func (s *Server) setOwnedScope(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	var accounts []store.DeclaredAccount
	if err := json.NewDecoder(r.Body).Decode(&accounts); err != nil {
		writeError(w, http.StatusBadRequest, "the declared scope must be a list of accounts")
		return
	}
	if err := s.Store.SetOwnedScope(accounts, store.DeclaredByInterface); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.ownedScope(w, r)
}

// previewScopeLimit caps the named repositories. A reader deciding whether to undeclare needs a
// few names to recognize, not a list nobody reads.
const previewScopeLimit = 20

// ScopePreview is what a candidate declaration would change, answered before it is written.
//
// Repositories are named rather than only counted: "417 repositories leave" is a number nobody
// can check, and recognizing one of them is how an operator catches a declaration they did not
// mean to make.
type ScopePreview struct {
	// Roots are the repositories a declaration names directly; the closure is everything those
	// reach. The two differ by the whole supply chain, so reporting one as the other would
	// understate what undeclaring costs.
	RootsNow    int `json:"rootsNow"`
	RootsNext   int `json:"rootsNext"`
	ClosureNow  int `json:"closureNow"`
	ClosureNext int `json:"closureNext"`

	Entering      []string `json:"entering"`
	Leaving       []string `json:"leaving"`
	EnteringCount int      `json:"enteringCount"`
	LeavingCount  int      `json:"leavingCount"`
	Truncated     bool     `json:"truncated"`

	// QueuedNow and QueuedNext are frontier rows inside the closure, which is what the crawl
	// will actually work. A scope can shrink without any queued work stopping, and an operator
	// reading only repository counts could not tell.
	QueuedNow  int `json:"queuedNow"`
	QueuedNext int `json:"queuedNext"`
}

// previewOwnedScope answers what a candidate declaration would admit and drop.
//
// A POST because it carries a body, and deliberately not control-guarded: it writes nothing, and
// guarding a read would tell the next reader that it does.
func (s *Server) previewOwnedScope(w http.ResponseWriter, r *http.Request) {
	var candidate []store.DeclaredAccount
	if err := json.NewDecoder(r.Body).Decode(&candidate); err != nil {
		writeError(w, http.StatusBadRequest, "the declared scope must be a list of accounts")
		return
	}

	out, err := s.scopePreview(candidate)
	writeJSON(w, out, err)
}

func (s *Server) scopePreview(candidate []store.DeclaredAccount) (ScopePreview, error) {
	var out ScopePreview

	repositories, err := s.Store.AllRepositories()
	if err != nil {
		return out, err
	}
	nextRoots := make([]string, 0, len(repositories))
	for _, full := range repositories {
		owner, repo, ok := strings.Cut(full, "/")
		if ok && store.CoveredBy(candidate, owner, repo) {
			nextRoots = append(nextRoots, full)
		}
	}

	builder := &graph.Builder{Store: s.Store}
	now, err := builder.OwnedClosure()
	if err != nil {
		return out, err
	}
	next, err := builder.ClosureFrom(nextRoots)
	if err != nil {
		return out, err
	}

	out.RootsNow, out.RootsNext = now.Roots(), next.Roots()
	out.ClosureNow, out.ClosureNext = now.Size(), next.Size()

	for _, name := range next.Repositories() {
		if !now.Contains(name) {
			out.EnteringCount++
			if len(out.Entering) < previewScopeLimit {
				out.Entering = append(out.Entering, name)
			}
		}
	}
	for _, name := range now.Repositories() {
		if !next.Contains(name) {
			out.LeavingCount++
			if len(out.Leaving) < previewScopeLimit {
				out.Leaving = append(out.Leaving, name)
			}
		}
	}
	out.Truncated = out.EnteringCount > len(out.Entering) || out.LeavingCount > len(out.Leaving)
	if out.Entering == nil {
		out.Entering = []string{}
	}
	if out.Leaving == nil {
		out.Leaving = []string{}
	}

	queued, err := s.Store.FrontierRepositoryCounts()
	if err != nil {
		return out, err
	}
	for full, n := range queued {
		if now.Contains(full) {
			out.QueuedNow += n
		}
		if next.Contains(full) {
			out.QueuedNext += n
		}
	}
	return out, nil
}

// refreshAccounts re-establishes the identity from gh.
//
// A failure leaves the stored accounts alone and reports the problem beside them. Wiping them
// would let a moment without a network silently redraw every figure the split appears in, and
// the operator would have no way to tell that from having genuinely lost access.
func (s *Server) refreshAccounts(w http.ResponseWriter, r *http.Request) {
	verify := s.Verify
	if verify == nil {
		verify = func(ctx context.Context) (ghauth.Identity, error) {
			return ghauth.Verify(ctx, ghauth.Exec)
		}
	}

	id, err := verify(r.Context())
	if err != nil {
		var p *ghauth.Problem
		if !errors.As(err, &p) {
			p = &ghauth.Problem{
				Reason: err.Error(),
				Remedy: "Check the authentication with: gh auth status",
			}
		}
		stored, readErr := s.Store.Accounts()
		writeJSON(w, view(stored, p), readErr)
		return
	}

	if err := s.Store.ReplaceAccounts(verification(id)); err != nil {
		writeJSON(w, nil, err)
		return
	}
	stored, err := s.Store.Accounts()
	writeJSON(w, view(stored, nil), err)
}

// verification maps what gh established onto what the store records. The two types are kept
// apart so the store never depends on how the identity was obtained.
func verification(id ghauth.Identity) store.Verification {
	v := store.Verification{
		Login: id.Login, Host: id.Host, VerifiedAt: id.VerifiedAt, Scopes: id.Scopes,
	}
	for _, a := range id.Accounts {
		v.Accounts = append(v.Accounts, store.Account{Owner: a.Owner, Kind: a.Kind, Role: a.Role})
	}
	return v
}

func (s *Server) ownerCandidates(w http.ResponseWriter, r *http.Request) {
	candidates, err := s.Store.OwnerCandidates()
	writeJSON(w, candidates, err)
}

func pinningFilter(r *http.Request) (store.PinningFilter, error) {
	q := r.URL.Query()
	scope, err := repositoryScope(r)
	if err != nil {
		return store.PinningFilter{}, err
	}
	return store.PinningFilter{
		Search:      q.Get("q"),
		RefStyle:    q.Get("style"),
		FlaggedOnly: q.Get("flagged") == "true",
		Scope:       scope,
	}, nil
}

// wholeNumber names the parameters that must parse as a whole number wherever they appear.
//
// Listed once rather than per route because the same word means the same thing on every endpoint
// that takes it, and a reader who writes `top=lots` and is handed the default has been answered
// with a question they did not ask.
var wholeNumber = map[string]bool{"top": true, "hops": true, "max": true}

// reading declares the query parameters an endpoint applies, and refuses every other one by name.
//
// The graph endpoints refuse an unknown term already, because they parse the shared predicate.
// These read their own parameters and so had nothing to refuse with: measured on a crawled
// organization, `/api/repositories?severity=error` returned all 7 matches and
// `/api/actions?properties=business_id:3` all 200 — the whole answer, wearing the name of a
// narrowed one.
func reading(h http.HandlerFunc, params ...string) http.HandlerFunc {
	applies := make(map[string]bool, len(params))
	for _, p := range params {
		applies[p] = true
	}
	return func(w http.ResponseWriter, r *http.Request) {
		for name, values := range r.URL.Query() {
			if !applies[name] {
				writeError(w, http.StatusBadRequest, misdirected(name))
				return
			}
			if !wholeNumber[name] {
				continue
			}
			if n, err := strconv.Atoi(values[0]); err != nil || n < 0 {
				writeError(w, http.StatusBadRequest,
					fmt.Sprintf("%s must be a whole number, got %q", name, values[0]))
				return
			}
		}
		h(w, r)
	}
}

// misdirected is the wording for a parameter an endpoint does not apply.
//
// Three different things arrive here and a reader needs them told apart: a term of the filter
// language that another surface answers, a term nothing answers, and a name ABOM has never had.
// One wording for all three sends somebody hunting for a surface that does not exist, or hunting
// for a typo in a word that was spelled correctly.
func misdirected(name string) string {
	switch {
	case viewspec.AppliedByExplorer(name):
		return fmt.Sprintf("this list does not narrow by %q. The Explorer does.", name)
	case viewspec.IsTerm(name):
		return fmt.Sprintf("this list does not narrow by %q, and no surface here does.", name)
	default:
		return fmt.Sprintf("ABOM has no filter called %q.", name)
	}
}

func (s *Server) frontier(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.FrontierSnapshot(
		r.URL.Query().Get("status"), intParam(r, "top", 500))
	writeJSON(w, items, err)
}

// actions lists one page of the inventory, ordered across the whole match.
//
// The predicate is the shared one, so a scope built on any surface narrows this listing too, and a
// term the listing cannot apply is refused by name rather than dropped. `top`, `offset`, `sort` and
// `desc` are cursors into the answer rather than clauses of the question.
func (s *Server) actions(w http.ResponseWriter, r *http.Request) {
	spec, err := s.graphPredicate(r, "top", "offset", "sort", "desc")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsInventory(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, unlistable(refused))
		return
	}
	query := store.InventoryQuery{
		Search:   spec.Search,
		Kinds:    spec.Kinds,
		Origins:  spec.Origins,
		MinReach: spec.MinReach,
		Scope:    spec.Scope,
		Flags:    spec.Flags,
		RefStyle: spec.RefStyle,
		Rules:    spec.Rules,
		NotRules: spec.NotRules,
		Provider: spec.Provider,
		Severity: spec.Severity,
		Mode:     spec.Mode,

		Repositories: spec.Repositories,

		Sort:     r.URL.Query().Get("sort"),
		Desc:     r.URL.Query().Get("desc") == "true",
		Offset:   intParam(r, "offset", 0),
		Limit:    intParam(r, "top", 50),
		Grouping: scopeFromSpec(spec),
	}
	// The same closure the graph and pruning read, so what a listing shows and what removal would
	// keep are one answer rather than two that drift.
	if query.Scope == store.ScopeOwned {
		keep, _, err := (&graph.Builder{Store: s.Store}).KeepList()
		if err != nil {
			writeJSON(w, store.InventoryPage{}, err)
			return
		}
		query.Closure = keep
	}
	page, err := s.Store.ActionsInventoryPage(query)
	writeJSON(w, page, err)
}

// relationships lists one page of declared relationships, ordered across the whole match.
//
// The unit the rest of the language could not answer: every other listing is a set of nodes, and a
// boundary, a reference style and a route are facts about the line between two of them. Containment
// is absent because nothing declares it, so the total here counts what a reader could go and edit.
func (s *Server) relationships(w http.ResponseWriter, r *http.Request) {
	spec, err := s.graphPredicate(r, "top", "offset", "sort", "desc")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsRelationships(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, strings.Join(refused, ", ")+
			" describes a node or a finding, which a list of relationships cannot answer. "+
			"The Explorer does.")
		return
	}
	query := store.RelationshipsQuery{
		Search:    spec.Search,
		EdgeKinds: spec.EdgeKinds,
		Boundary:  spec.Boundary,
		RefStyle:  spec.RefStyle,
		Rules:     spec.Rules,
		NotRules:  spec.NotRules,
		Scope:     spec.Scope,
		Mode:      spec.Mode,
		Sort:      r.URL.Query().Get("sort"),
		Desc:      r.URL.Query().Get("desc") == "true",
		Offset:    intParam(r, "offset", 0),
		Limit:     intParam(r, "top", 50),
		Grouping:  scopeFromSpec(spec),
	}
	// The same closure every other unit reads, so "what you own" means one population across them.
	if query.Scope == store.ScopeOwned {
		keep, _, err := (&graph.Builder{Store: s.Store}).KeepList()
		if err != nil {
			writeJSON(w, store.RelationshipsPage{}, err)
			return
		}
		query.Closure = keep
	}
	page, err := s.Store.RelationshipsPage(query)
	writeJSON(w, page, err)
}

// unlistable is the wording for a term a node listing cannot apply.
//
// It names the Explorer rather than the endpoint behind it: a reader who has been refused is a
// reader who asked a reasonable question in the wrong place, and an API route is not a place they
// can go.
func unlistable(terms []string) string {
	return strings.Join(terms, ", ") +
		" describes a relationship or a finding, which a list of nodes cannot answer. " +
		"The Explorer does."
}

// repositories lists one page of the repositories unit, ordered across the whole match.
//
// A unit of its own rather than a kind in the node listing: a repository's measurements are its
// own, and a `repo:` node carries none of them.
func (s *Server) repositories(w http.ResponseWriter, r *http.Request) {
	spec, err := s.graphPredicate(r, "top", "offset", "sort", "desc")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsRepositories(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, unlistable(refused))
		return
	}
	query := store.RepositoriesQuery{
		Search:   spec.Search,
		Scope:    spec.Scope,
		Sort:     r.URL.Query().Get("sort"),
		Desc:     r.URL.Query().Get("desc") == "true",
		Offset:   intParam(r, "offset", 0),
		Limit:    intParam(r, "top", 50),
		Grouping: scopeFromSpec(spec),
	}
	// The same closure the node listing reads, so "what you own" means one population across units.
	if query.Scope == store.ScopeOwned {
		keep, _, err := (&graph.Builder{Store: s.Store}).KeepList()
		if err != nil {
			writeJSON(w, store.RepositoriesPage{}, err)
			return
		}
		query.Closure = keep
	}
	page, err := s.Store.RepositoriesPage(query)
	writeJSON(w, page, err)
}

// workflows and jobs list the units whose columns are their own.
//
// Separate endpoints rather than a unit parameter on /api/actions, because a unit asks for its own
// columns: a workflow has jobs, steps and triggers, and none of them fits the shape a caller
// expecting an action summary would parse.
func (s *Server) workflows(w http.ResponseWriter, r *http.Request) {
	req, ok := s.unitRequest(w, r, viewspec.Spec.UnenforceableAsWorkflows)
	if !ok {
		return
	}
	page, err := s.Store.WorkflowsPage(store.WorkflowsQuery{
		Search: req.Spec.Search, Scope: req.Spec.Scope, Flags: req.Spec.Flags,
		Closure: req.Closure, Sort: req.Sort, Desc: req.Desc,
		Offset: req.Offset, Limit: req.Limit, Grouping: req.Grouping,
	})
	writeJSON(w, page, err)
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	req, ok := s.unitRequest(w, r, viewspec.Spec.UnenforceableAsJobs)
	if !ok {
		return
	}
	page, err := s.Store.JobsPage(store.JobsQuery{
		Search: req.Spec.Search, Scope: req.Spec.Scope, Flags: req.Spec.Flags,
		Closure: req.Closure, Sort: req.Sort, Desc: req.Desc,
		Offset: req.Offset, Limit: req.Limit, Grouping: req.Grouping,
	})
	writeJSON(w, page, err)
}

// unitRequest is everything a unit listing reads from a request, parsed once.
type unitRequest struct {
	Spec     viewspec.Spec
	Sort     string
	Desc     bool
	Offset   int
	Limit    int
	Grouping store.RepositoryScope
	Closure  []string
}

// unitRequest parses the predicate, refuses the terms this unit cannot apply, and resolves the
// owned closure when the scope names one. It answers false once it has written the refusal, so a
// handler that ignored the flag could not accidentally serve an unscoped answer.
func (s *Server) unitRequest(
	w http.ResponseWriter, r *http.Request, refuse func(viewspec.Spec) []string,
) (unitRequest, bool) {
	spec, err := s.graphPredicate(r, "top", "offset", "sort", "desc")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return unitRequest{}, false
	}
	if refused := refuse(spec); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, unlistable(refused))
		return unitRequest{}, false
	}
	req := unitRequest{
		Spec:     spec,
		Sort:     r.URL.Query().Get("sort"),
		Desc:     r.URL.Query().Get("desc") == "true",
		Offset:   intParam(r, "offset", 0),
		Limit:    intParam(r, "top", 50),
		Grouping: scopeFromSpec(spec),
	}
	// The same closure the node listing reads, so "what you own" means one population across units.
	if spec.Scope == store.ScopeOwned {
		keep, _, err := (&graph.Builder{Store: s.Store}).KeepList()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return unitRequest{}, false
		}
		req.Closure = keep
	}
	return req, true
}

// repositoryScope reads the grouping terms through the shared parser, so a malformed one is
// refused here in the same words the graph and the gate refuse it in.
func repositoryScope(r *http.Request) (store.RepositoryScope, error) {
	include, exclude, err := propertyMatches(r.URL.Query())
	if err != nil {
		return store.RepositoryScope{}, err
	}
	return store.RepositoryScope{Include: include, Exclude: exclude}, nil
}

// scopeFromSpec takes the property terms off a parsed predicate, so an endpoint reading the shared
// predicate does not parse the same two parameters a second time and disagree about them.
func scopeFromSpec(spec viewspec.Spec) store.RepositoryScope {
	return store.RepositoryScope{Include: spec.Properties, Exclude: spec.NotProperties}
}

func (s *Server) repository(w http.ResponseWriter, r *http.Request) {
	full := r.PathValue("owner") + "/" + r.PathValue("name")
	detail, err := s.Store.RepositoryDetailOf(full)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, detail, nil)
}

func intParam(r *http.Request, name string, fallback int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

// writeJSON renders a successful result, or the error that prevented it.
//
// Results are marshaled before anything is written, so a failure midway through cannot leave a
// half-written body that a client would parse as truncated data.
func writeJSON(w http.ResponseWriter, payload any, err error) {
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	started := time.Now()
	body, err := json.Marshal(payload)
	if rec, ok := w.(*recorder); ok {
		rec.marshalNanos = time.Since(started).Nanoseconds()
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("encoding response: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// The body is already serialized, so a short write means the client went away.
	_, _ = w.Write(body)
}

// gzipped compresses responses for clients that accept it.
//
// The whole-corpus payload is several megabytes of highly repetitive JSON, and shipping it
// uncompressed is the difference between a usable first load and a visible stall.
func gzipped(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		zw := gzip.NewWriter(w)
		defer func() { _ = zw.Close() }()
		next.ServeHTTP(gzipWriter{ResponseWriter: w, Writer: zw}, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	io.Writer
}

func (g gzipWriter) Write(b []byte) (int, error) { return g.Writer.Write(b) }

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is already sent; there is no second error to report.
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
