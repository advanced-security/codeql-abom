package serve

import (
	"errors"
	"net/http"

	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// PruneView is what removal would cost, or what it just cost.
//
// Declared and Reachable are stated beside the removal because they are what decides it: a corpus
// looks enormous until you see that one declared repository reaches 270 of its 1,099, and that
// the rest belongs to somebody else.
type PruneView struct {
	Declared  int               `json:"declared"`
	Reachable int               `json:"reachable"`
	Removed   store.PruneReport `json:"removed"`
	// Tombstoned is what has already been removed from view and is still on disk. Without it the
	// interface cannot tell whether offering to delete permanently would do anything at all.
	Tombstoned int `json:"tombstoned"`
	// Applied distinguishes "this is what it would cost" from "this is what it cost". The same
	// shape answering both would let a page report an estimate as an outcome.
	Applied bool `json:"applied"`
	// Unavailable names why no removal can be measured, and is empty when one can. A zero report
	// means "nothing is outside the closure"; this means "nothing can be said about it", and a
	// page showing the first for the second reports an unmeasured corpus as a clean one.
	Unavailable string `json:"unavailable,omitempty"`
}

// prune explains what leaving the declared closure would remove, changing nothing.
func (s *Server) prune(w http.ResponseWriter, r *http.Request) {
	out, err := s.pruneReport(false)
	// Asking the question changes nothing, so an empty closure is an answer rather than a fault.
	// Returned as a 500 it reached the Discovery page as "could not reach the server", on the one
	// visit — the first — where nothing has gone wrong.
	if errors.Is(err, store.ErrEmptyClosure) {
		out.Unavailable = "Nothing has been enumerated yet, so there is no closure to be outside of."
		writeJSON(w, out, nil)
		return
	}
	writeJSON(w, out, err)
}

// pruneApply tombstones what the declaration does not reach. Reversible only by re-analysis, so
// it carries the same guard as starting discovery.
func (s *Server) pruneApply(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	out, err := s.pruneReport(true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, out, nil)
}

// pruneCompact deletes what has already been tombstoned and bumps the epoch, which makes every
// open page reload. Not reversible at all.
func (s *Server) pruneCompact(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	report, err := s.Store.Compact()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, PruneView{Removed: report, Applied: true}, nil)
}

func (s *Server) pruneReport(apply bool) (PruneView, error) {
	var out PruneView

	keep, closure, err := (&graph.Builder{Store: s.Store}).KeepList()
	if err != nil {
		return out, err
	}
	out.Declared, out.Reachable = closure.Roots(), closure.Size()

	report, err := s.Store.ExplainOutsideClosure(keep)
	if apply {
		report, err = s.Store.MarkOutsideClosure(keep)
	}
	if err != nil {
		return out, err
	}
	out.Removed, out.Applied = report, apply

	out.Tombstoned, err = s.Store.Tombstoned()
	if err != nil {
		return out, err
	}
	return out, nil
}
