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
	"strconv"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
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
	// AllowRemoteControl lets a non-loopback client start discovery. Off by default because
	// starting it makes this process clone repositories and run CodeQL.
	AllowRemoteControl bool
	// Log receives failed requests. Nil discards them, which leaves a 500 visible only to the
	// client that hit it.
	Log io.Writer
}

// Handler builds the routing tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/summary", s.summary)
	mux.HandleFunc("GET /api/remediation", s.remediation)
	mux.HandleFunc("GET /api/advisories", s.advisories)
	mux.HandleFunc("GET /api/exposure", s.exposure)
	mux.HandleFunc("GET /api/graph", s.graph)
	mux.HandleFunc("GET /api/graph/full", s.fullGraph)
	mux.HandleFunc("GET /api/graph/version", s.graphVersion)
	mux.HandleFunc("GET /api/graph/delta", s.graphDelta)
	mux.HandleFunc("GET /api/discovery/status", s.discoveryStatus)
	mux.HandleFunc("POST /api/discovery/start", s.discoveryStart)
	mux.HandleFunc("POST /api/discovery/pause", s.discoveryPause)
	mux.HandleFunc("POST /api/discovery/requeue/{tier}", s.discoveryRequeue)
	mux.HandleFunc("GET /api/discovery/suites", s.tierSuites)
	mux.HandleFunc("PUT /api/discovery/suites/{tier}", s.setTierSuites)
	mux.HandleFunc("GET /api/nodes", s.nodes)
	mux.HandleFunc("GET /api/node/{key...}", s.node)
	mux.HandleFunc("GET /api/pinning", s.pinning)
	mux.HandleFunc("GET /api/pinning/totals", s.pinningTotals)
	mux.HandleFunc("GET /api/accounts", s.accounts)
	mux.HandleFunc("POST /api/accounts/refresh", s.refreshAccounts)
	mux.HandleFunc("GET /api/owner-candidates", s.ownerCandidates)
	mux.HandleFunc("GET /api/frontier", s.frontier)
	mux.HandleFunc("GET /api/actions", s.actions)
	mux.HandleFunc("GET /api/repositories", s.repositories)
	mux.HandleFunc("GET /api/repositories/{owner}/{name}", s.repository)

	if s.UI != nil {
		// Registered for every method rather than GET alone: a method-restricted catch-all
		// cannot coexist with the /api/ pattern below, which ServeMux rejects outright.
		mux.Handle("/", s.ui())
	}
	// Anything under /api/ that no route claimed is absent, whatever the method. Without this a
	// withdrawn route answers a PUT with 405, which says the resource is still there.
	mux.HandleFunc("/api/", http.NotFound)
	return gzipped(s.logging(mux))
}

// logging records requests that failed, and ones that succeeded too slowly to be pleasant.
//
// Handlers answer an error to the caller and nothing else, so a query that breaks for every
// reader leaves no trace on the machine actually running. Successful requests are not logged:
// the interface polls continuously, and a line each would bury the failures.
func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Log == nil {
			next.ServeHTTP(w, r)
			return
		}
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		took := time.Since(start)
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

// recorder keeps the status and, for a failure, the body that explains it.
type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	// Only a failure's body is kept; a successful response is megabytes of graph.
	if r.status >= http.StatusBadRequest && r.body.Len() < 2048 {
		r.body.Write(b)
	}
	return r.ResponseWriter.Write(b)
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

func (s *Server) remediation(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.RemediationPriority(intParam(r, "top", 50))
	writeJSON(w, items, err)
}

func (s *Server) advisories(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.AdvisoriesByReach(intParam(r, "top", 50))
	writeJSON(w, items, err)
}

func (s *Server) exposure(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("by") {
	case "repository", "repo":
		items, err := s.Store.ExposuresByRepository(intParam(r, "top", 200))
		writeJSON(w, items, err)
	default:
		items, err := s.Store.ExposureByAdvisory()
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

	g, err := s.Store.Subgraph(store.SubgraphOptions{
		Focus:       focus,
		Hops:        intParam(r, "hops", 1),
		MaxNodes:    intParam(r, "max", store.DefaultSubgraphNodes),
		Direction:   q.Get("direction"),
		Granularity: q.Get("granularity"),
		Expand:      q.Get("expand") == "true",
	})
	writeJSON(w, g, err)
}

// fullGraph ships the entire corpus in one payload.
//
// The interface filters this in the browser, so the cost is paid once on arrival rather than on
// every interaction. Steps are folded into their workflow unless asked for: 3.6k nodes against
// 23k, for the same set of relationships.
func (s *Server) fullGraph(w http.ResponseWriter, r *http.Request) {
	g, err := s.Store.FullGraph(store.FullGraphOptions{
		IncludeSteps: r.URL.Query().Get("steps") == "true",
		Scope:        r.URL.Query().Get("scope"),
	})
	writeJSON(w, g, err)
}

func (s *Server) graphVersion(w http.ResponseWriter, r *http.Request) {
	version, err := s.Store.GraphVersion()
	writeJSON(w, version, err)
}

func (s *Server) graphDelta(w http.ResponseWriter, r *http.Request) {
	delta, err := s.Store.GraphDelta(int64(intParam(r, "since", 0)), store.FullGraphOptions{
		IncludeSteps: r.URL.Query().Get("steps") == "true",
	})
	writeJSON(w, delta, err)
}

func (s *Server) nodes(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.SearchNodes(r.URL.Query().Get("q"), intParam(r, "top", 50))
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

	detail, err := s.Store.NodeDetailOf(key)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, detail, nil)
}

func (s *Server) pinning(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.PinningLevel(r.URL.Query().Get("scope"), r.URL.Query().Get("parent"),
		pinningFilter(r), intParam(r, "top", 1000))
	writeJSON(w, rows, err)
}

func (s *Server) pinningTotals(w http.ResponseWriter, r *http.Request) {
	total, err := s.Store.PinningTotals(pinningFilter(r))
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
}

func view(v store.Verification, problem *ghauth.Problem) accountsView {
	out := accountsView{
		Authenticated: v.Login != "",
		Login:         v.Login,
		Host:          v.Host,
		Accounts:      v.Accounts,
		Problem:       problem,
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
	v := store.Verification{Login: id.Login, Host: id.Host, VerifiedAt: id.VerifiedAt}
	for _, a := range id.Accounts {
		v.Accounts = append(v.Accounts, store.Account{Owner: a.Owner, Kind: a.Kind, Role: a.Role})
	}
	return v
}

func (s *Server) ownerCandidates(w http.ResponseWriter, r *http.Request) {
	candidates, err := s.Store.OwnerCandidates()
	writeJSON(w, candidates, err)
}

func pinningFilter(r *http.Request) store.PinningFilter {
	q := r.URL.Query()
	return store.PinningFilter{
		Search:      q.Get("q"),
		RefStyle:    q.Get("style"),
		FlaggedOnly: q.Get("flagged") == "true",
	}
}

func (s *Server) frontier(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.FrontierSnapshot(
		r.URL.Query().Get("status"), intParam(r, "top", 500))
	writeJSON(w, items, err)
}

func (s *Server) actions(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ActionsInventory(r.URL.Query().Get("q"), intParam(r, "top", 200))
	writeJSON(w, items, err)
}

func (s *Server) repositories(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.RepositoriesOverview(r.URL.Query().Get("q"), intParam(r, "top", 500))
	writeJSON(w, items, err)
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
	body, err := json.Marshal(payload)
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
