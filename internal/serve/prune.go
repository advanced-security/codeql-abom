package serve

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/checkouts"
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
	// Live is what is in view, so a removal reads as a share of it rather than as a bare count.
	Live store.LiveSize `json:"live"`
	// Majority is a removal taking at least half of what is in view, nodes or edges. It is applied
	// only when said outright: a scope that could not be confirmed reads exactly like one that owns
	// little, and both put most of the graph outside it.
	Majority bool `json:"majority"`
	// Proof names this preview. A removal is applied only to a preview somebody read, and only
	// while nothing that preview kept would go.
	Proof string `json:"proof,omitempty"`
	// Checkouts are the working copies the removal deletes from disk, named because a deletion is
	// owed its list.
	Checkouts []string `json:"checkouts"`
}

// PruneConflict is a removal that did not happen because its preview no longer holds, with the
// removal as it now stands, under a proof of its own, to review in its place.
type PruneConflict struct {
	Error string    `json:"error"`
	View  PruneView `json:"view"`
}

// pruneReview is what one preview kept, held until it is applied or expires.
type pruneReview struct {
	proof string
	keep  map[string]bool
	at    time.Time
}

const (
	pruneReviewsHeld = 16
	pruneReviewLife  = time.Hour
)

// prune explains what leaving the declared closure would remove, changing nothing.
func (s *Server) prune(w http.ResponseWriter, r *http.Request) {
	keep, out, err := s.prunePreview()
	// Asking the question changes nothing, so a closure that cannot be pruned is an answer rather
	// than a fault. Returned as a 500 it reached the Discovery page as "could not reach the
	// server", on the one visit — the first — where nothing has gone wrong.
	if unavailable := pruneUnavailable(err); unavailable != "" {
		out.Unavailable = unavailable
		writeJSON(w, out, nil)
		return
	}
	if err == nil {
		out.Proof = s.rememberPrune(keep)
	}
	writeJSON(w, out, err)
}

func pruneUnavailable(err error) string {
	switch {
	case errors.Is(err, store.ErrEmptyClosure):
		return "Nothing has been enumerated yet, so there is no closure to be outside of."
	case errors.Is(err, store.ErrUnanalyzedClosure):
		return "Nothing you declared has been analyzed yet, so what it reaches is not " +
			"known. Removing now would take out everything it is going to reach."
	}
	return ""
}

// prunePreview reads what a removal would take now, with the keep list it was read against.
func (s *Server) prunePreview() ([]string, PruneView, error) {
	out := PruneView{Checkouts: []string{}}
	keep, closure, err := (&graph.Builder{Store: s.Store}).KeepList()
	if err != nil {
		return nil, out, err
	}
	out.Declared, out.Reachable = closure.Roots(), closure.Size()
	if out.Removed, err = s.Store.ExplainOutsideClosure(keep); err != nil {
		return keep, out, err
	}
	if out.Live, err = s.Store.LiveGraph(); err != nil {
		return keep, out, err
	}
	out.Majority = out.Removed.TakesMostOf(out.Live)
	if out.Tombstoned, err = s.Store.Tombstoned(); err != nil {
		return keep, out, err
	}
	if paths, err := s.staleCheckoutPathsFor(keep); err == nil {
		for _, p := range paths {
			out.Checkouts = append(out.Checkouts, filepath.Base(p))
		}
	}
	return keep, out, nil
}

// pruneApply tombstones what the declaration does not reach. Reversible only by re-analysis, so
// it carries the same guard as starting discovery, and it applies only a preview somebody read:
// the scope is read again when the button is pressed, and between the two it can shrink — an
// organization a later `gh` check could not see stops counting as owned — so everything the
// preview kept has to be kept still.
func (s *Server) pruneApply(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	var asked struct {
		Proof    string `json:"proof"`
		Majority bool   `json:"majority"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&asked); err != nil || asked.Proof == "" {
		writeError(w, http.StatusBadRequest, "nothing was removed: a removal names the preview it applies, "+
			"and this named none; read what would be removed first")
		return
	}
	reviewed, held := s.takePrune(asked.Proof)

	keep, now, err := s.prunePreview()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	lost := dropped(reviewed, keep)
	switch {
	case !held:
		s.refusePrune(w, keep, now, "Nothing was removed: that preview is no longer held. It was applied "+
			"already, it expired, or the server restarted. Review what would be removed now.")
		return
	case len(lost) > 0:
		s.refusePrune(w, keep, now, fmt.Sprintf("Nothing was removed: since it was reviewed, what you own "+
			"changed or could not be confirmed, and %d %s it kept would go: %s. Review what would be removed now.",
			len(lost), plural(int64(len(lost)), "repository", "repositories"), sample(lost, 3)))
		return
	case now.Majority && !asked.Majority:
		s.refusePrune(w, keep, now, "Nothing was removed: this takes most of what ABOM holds. A scope "+
			"that could not be confirmed reads exactly like one that owns little; remove this only if "+
			"that is what you mean, and say so.")
		return
	}

	// Resolved before the marking, not after: marking tombstones exactly these rows, and once it
	// has, nothing reads as both outside the closure and live, so the working copies would be
	// invisible a moment after they became dead weight.
	paths, pathErr := s.staleCheckoutPathsFor(keep)
	removed, err := s.Store.MarkOutsideClosure(keep)
	if err != nil {
		// The marking is not one statement, so a failure part way may have taken some of it.
		writeError(w, http.StatusInternalServerError, fmt.Sprintf(
			"removing what is outside your scope failed, and part of it may already be out of view: %v", err))
		return
	}
	// The working copies go with the rows. Leaving them is a corpus that shrinks while the disk
	// stays exactly as full, and nothing else ever removes them.
	if pathErr == nil && len(paths) > 0 {
		if _, err := checkouts.Remove(s.WorkDir, paths); err != nil && s.Log != nil {
			fmt.Fprintf(s.Log, "serve: removing working copies: %v\n", err)
		}
	}
	now.Removed, now.Applied, now.Proof = removed, true, ""
	if now.Tombstoned, err = s.Store.Tombstoned(); err == nil {
		now.Live, err = s.Store.LiveGraph()
	}
	if err != nil {
		// The removal happened; only the reading after it failed, and saying the first matters more.
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("removed %d %s and %d %s from view, then could not read what is left: %v",
			removed.Nodes, plural(int64(removed.Nodes), "node", "nodes"), removed.Edges, plural(int64(removed.Edges), "edge", "edges"), err))
		return
	}
	writeJSON(w, now, nil)
}

// staleCheckoutPathsFor resolves the working copies of repositories outside the kept list afresh
// rather than trusting a recorded list: a path held from an earlier read may name something that
// has since been refetched. Empty with no work directory configured, which is nothing known
// rather than none.
func (s *Server) staleCheckoutPathsFor(keep []string) ([]string, error) {
	if s.WorkDir == "" {
		return nil, nil
	}
	repos, err := s.Store.RepositoriesOutsideClosure(keep)
	if err != nil {
		return nil, err
	}
	return checkouts.Stale(s.WorkDir, repos)
}

// refusePrune answers a removal that did not happen with the removal as it now stands, under a
// proof of its own, so the next decision is made about what is true now.
func (s *Server) refusePrune(w http.ResponseWriter, keep []string, now PruneView, reason string) {
	now.Proof = s.rememberPrune(keep)
	body, err := json.Marshal(PruneConflict{Error: reason, View: now})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("encoding response: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_, _ = w.Write(body)
}

// rememberPrune holds what a preview kept under a proof nobody can name without reading it.
func (s *Server) rememberPrune(keep []string) string {
	review := pruneReview{proof: rand.Text(), keep: make(map[string]bool, len(keep)), at: time.Now()}
	for _, name := range keep {
		review.keep[strings.ToLower(name)] = true
	}
	s.pruneMu.Lock()
	defer s.pruneMu.Unlock()
	held := make([]pruneReview, 0, pruneReviewsHeld)
	for _, r := range s.pruneReviews {
		if time.Since(r.at) < pruneReviewLife {
			held = append(held, r)
		}
	}
	if len(held) >= pruneReviewsHeld {
		held = held[len(held)-pruneReviewsHeld+1:]
	}
	s.pruneReviews = append(held, review)
	return review.proof
}

// takePrune hands back what a preview kept and forgets it: one preview names one decision.
func (s *Server) takePrune(proof string) (map[string]bool, bool) {
	s.pruneMu.Lock()
	defer s.pruneMu.Unlock()
	for i, r := range s.pruneReviews {
		if r.proof == proof {
			s.pruneReviews = append(s.pruneReviews[:i:i], s.pruneReviews[i+1:]...)
			return r.keep, time.Since(r.at) < pruneReviewLife
		}
	}
	return nil, false
}

// dropped names what a preview kept that the scope no longer keeps, sorted.
func dropped(reviewed map[string]bool, keep []string) []string {
	now := make(map[string]bool, len(keep))
	for _, name := range keep {
		now[strings.ToLower(name)] = true
	}
	var out []string
	for name := range reviewed {
		if !now[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func sample(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:n], ", ") + fmt.Sprintf(" and %d more", len(names)-n)
}

// pruneCompact deletes what has already been tombstoned and bumps the epoch, which makes every
// open page reload. Not reversible at all.
func (s *Server) pruneCompact(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	report, err := s.Store.Compact()
	if err != nil {
		// Never a refusal: the epoch bump follows the deletion, so this can come after one happened.
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, PruneView{Removed: report, Applied: true, Checkouts: []string{}}, nil)
}
