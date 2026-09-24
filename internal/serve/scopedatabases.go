package serve

import (
	"errors"
	"net/http"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// databases answers which databases a scope would have to be run against, and which it cannot.
//
// The run's own resolution, deliberately: what a reader is shown before pressing Run is what the
// run covers. It builds nothing and schedules nothing, so a reader can see the size of a thing
// before deciding whether it is worth doing.
func (s *Server) databases(w http.ResponseWriter, r *http.Request) {
	spec, err := s.runPredicate(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	set, err := s.runSet(spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, set, nil)
}

// runPredicate reads the scope a run is asked over, refusing by name a term no run can apply.
func (s *Server) runPredicate(r *http.Request) (viewspec.Spec, error) {
	spec, err := s.graphPredicate(r)
	if err != nil {
		return spec, err
	}
	if refused := spec.UnenforceableAsRun(); len(refused) > 0 {
		return spec, errors.New(strings.Join(refused, ", ") + " cannot narrow a run, which covers " +
			"repositories: as the Repositories unit lists them, named one by one, or where a " +
			"finding sits")
	}
	return spec, nil
}

// runSet resolves a run's scope to the databases it covers.
//
// A scope asking the repositories unit's question covers exactly what that unit lists, so a run's
// population is a tab a reader can open and count; names and findings terms narrow it. Without
// one, names and findings keep the set path's meaning.
func (s *Server) runSet(spec viewspec.Spec) (store.DatabaseSet, error) {
	sel := store.SelectorFor(spec)
	if !spec.AsksRepositoriesUnit() {
		return s.Store.DatabasesMatching(sel)
	}
	query, err := s.repositoriesQuery(spec)
	if err != nil {
		return store.DatabaseSet{}, err
	}
	return s.Store.DatabasesListed(query, sel)
}

// repositoriesQuery is the repositories unit's population for a predicate, shared by the unit and
// by a run, with the same closure the node listing reads so "what you own" is one population.
func (s *Server) repositoriesQuery(spec viewspec.Spec) (store.RepositoriesQuery, error) {
	query := store.RepositoriesQuery{
		Search: spec.Search, Scope: spec.Scope, Grouping: scopeFromSpec(spec),
	}
	if query.Scope == store.ScopeOwned {
		keep, _, err := (&graph.Builder{Store: s.Store}).KeepList()
		if err != nil {
			return query, err
		}
		query.Closure = keep
	}
	return query, nil
}
