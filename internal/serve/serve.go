// Package serve exposes the ABOM over HTTP for the user interface.
//
// Queries stay in Go against the full database rather than being shipped to the browser: the
// corpus reaches gigabytes at organisation scale, which no client can hold, and every read the
// interface needs already exists here.
package serve

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Server answers interface queries from an ABOM database.
type Server struct {
	Store *store.Store
	// UI serves the interface assets. Nil disables it, leaving only the API.
	UI fs.FS
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
	mux.HandleFunc("GET /api/nodes", s.nodes)
	mux.HandleFunc("GET /api/node/{key...}", s.node)
	mux.HandleFunc("GET /api/pinning", s.pinning)
	mux.HandleFunc("GET /api/pinning/totals", s.pinningTotals)
	mux.HandleFunc("GET /api/owned-orgs", s.ownedOrgs)
	mux.HandleFunc("PUT /api/owned-orgs", s.setOwnedOrgs)
	mux.HandleFunc("GET /api/owner-candidates", s.ownerCandidates)
	mux.HandleFunc("GET /api/frontier", s.frontier)
	mux.HandleFunc("GET /api/actions", s.actions)
	mux.HandleFunc("GET /api/repositories", s.repositories)
	mux.HandleFunc("GET /api/repositories/{owner}/{name}", s.repository)

	if s.UI != nil {
		mux.Handle("GET /", s.ui())
	}
	return gzipped(mux)
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
	})
	writeJSON(w, g, err)
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

func (s *Server) ownedOrgs(w http.ResponseWriter, r *http.Request) {
	owners, err := s.Store.OwnedOrgs()
	writeJSON(w, owners, err)
}

func (s *Server) setOwnedOrgs(w http.ResponseWriter, r *http.Request) {
	var owners []string
	// Bounded because the body is unauthenticated input; the declaration is a short list of
	// account names, never a payload.
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&owners); err != nil {
		http.Error(w, "expected a JSON array of account names", http.StatusBadRequest)
		return
	}
	if err := s.Store.SetOwnedOrgs(owners); err != nil {
		writeJSON(w, nil, err)
		return
	}
	stored, err := s.Store.OwnedOrgs()
	writeJSON(w, stored, err)
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
// Results are marshalled before anything is written, so a failure midway through cannot leave a
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
	w.Write(body)
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
		defer zw.Close()
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
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
