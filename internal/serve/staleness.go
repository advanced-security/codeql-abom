package serve

import (
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/advanced-security/codeql-abom/internal/checkouts"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Staleness is the last answer to what no longer belongs, and when it was asked.
//
// Held rather than computed per request: the walk behind it covers the whole closure, and the page
// that reads it renders on first paint.
type Staleness struct {
	// Checked separates "nothing has looked" from "nothing needs removing". The second is a
	// measurement and the first is the absence of one, and a page showing the second for the first
	// tells an operator their corpus is tidy before anything examined it.
	Checked   bool      `json:"checked"`
	CheckedAt string    `json:"checkedAt,omitempty"`
	View      PruneView `json:"view"`
	// Unavailable names why no removal could be measured, and is empty when one could.
	Unavailable string `json:"unavailable,omitempty"`
	// Checkouts are working copies on disk for repositories the closure no longer reaches. Named
	// rather than counted because removing one is a deletion, and whoever accepts it is entitled
	// to know what goes.
	Checkouts []string `json:"checkouts"`
}

// CheckStaleness records what leaving the declared closure would remove, changing nothing.
//
// Synchronous, so the caller decides when it runs. Startup runs it in a goroutine: the closure
// walk is not something an operator should wait through to reach the interface.
func (s *Server) CheckStaleness() {
	out := Staleness{Checked: true, CheckedAt: time.Now().UTC().Format(time.RFC3339), Checkouts: []string{}}
	view, err := s.pruneReport(false)
	switch {
	case errors.Is(err, store.ErrEmptyClosure):
		out.Unavailable = "Nothing has been enumerated yet, so there is no closure to be outside of."
	case errors.Is(err, store.ErrUnanalyzedClosure):
		out.Unavailable = "Nothing you declared has been analyzed yet, so what it reaches is not " +
			"known. Removing now would take out everything it is going to reach."
	case err != nil:
		out.Unavailable = err.Error()
	default:
		out.View = view
		out.Checkouts = s.staleCheckouts()
	}
	s.staleMu.Lock()
	s.stale = out
	s.staleMu.Unlock()
}

// staleCheckouts names the working copies on disk belonging to repositories the closure no longer
// reaches. Empty with no work directory configured, which is nothing known rather than none.
func (s *Server) staleCheckouts() []string {
	paths, err := s.staleCheckoutPaths()
	if err != nil {
		return []string{}
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

// staleCheckoutPaths resolves the directories afresh rather than trusting a recorded list: a path
// held from an earlier check may name something that has since been refetched.
func (s *Server) staleCheckoutPaths() ([]string, error) {
	if s.WorkDir == "" {
		return nil, nil
	}
	keep, _, err := (&graph.Builder{Store: s.Store}).KeepList()
	if err != nil {
		return nil, err
	}
	repos, err := s.Store.RepositoriesOutsideClosure(keep)
	if err != nil {
		return nil, err
	}
	return checkouts.Stale(s.WorkDir, repos)
}

// staleness answers what the last check found, whether or not one has finished.
func (s *Server) staleness(w http.ResponseWriter, _ *http.Request) {
	s.staleMu.RLock()
	out := s.stale
	s.staleMu.RUnlock()
	writeJSON(w, out, nil)
}
