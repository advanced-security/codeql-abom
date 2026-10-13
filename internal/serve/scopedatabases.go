package serve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

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
	set, err := s.runSet(r.Context(), spec)
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

// runSet resolves a run's scope to the databases it covers, as the CodeQL running now would.
//
// A scope asking the repositories unit's question covers exactly what that unit lists, so a run's
// population is a tab a reader can open and count; names and findings terms narrow it. Without
// one, names and findings keep the set path's meaning.
func (s *Server) runSet(ctx context.Context, spec viewspec.Spec) (store.DatabaseSet, error) {
	sel := store.SelectorFor(spec)
	var set store.DatabaseSet
	var err error
	if spec.AsksRepositoriesUnit() {
		set, err = s.Store.DatabasesListed(repositoriesQuery(spec), sel)
	} else {
		set, err = s.Store.DatabasesMatching(sel)
	}
	if err != nil {
		return set, err
	}
	cli, err := s.codeQL(ctx)
	if err != nil {
		return set, fmt.Errorf("which CodeQL would run this cannot be read, so nothing held "+
			"can be counted as usable: %w", err)
	}
	return s.servedOnly(set.BuiltBy(cli)), nil
}

// servedOnly moves what this instance cannot fetch out of the run, naming each.
func (s *Server) servedOnly(set store.DatabaseSet) store.DatabaseSet {
	if set.Outside == nil {
		set.Outside = []store.DatabaseGap{}
	}
	if s.Served == nil {
		return set
	}
	const why = "not a repository in this Demo's simulated GitHub: upstream repositories are " +
		"listed, never served; run over the repositories that use it instead"
	databases := make([]store.ScopedDatabase, 0, len(set.Databases))
	for _, d := range set.Databases {
		if s.Served(d.Repository) {
			databases = append(databases, d)
		} else {
			set.Outside = append(set.Outside, store.DatabaseGap{Repository: d.Repository, Reason: why})
		}
	}
	gaps := make([]store.DatabaseGap, 0, len(set.Gaps))
	for _, g := range set.Gaps {
		if s.Served(g.Repository) {
			gaps = append(gaps, g)
		} else {
			set.Outside = append(set.Outside, store.DatabaseGap{Repository: g.Repository, Reason: why})
		}
	}
	set.Databases, set.Gaps = databases, gaps
	if len(databases)+len(gaps) == 0 && len(set.Outside) > 0 {
		names := make([]string, 0, len(set.Outside))
		for _, o := range set.Outside {
			names = append(names, o.Repository)
		}
		set.Unresolved = strings.Join(names, ", ") + " is not in this Demo's simulated GitHub, so " +
			"no run can build it; run over the repositories that use it instead"
	}
	return set
}

// repositoriesQuery is the repositories unit's population for a predicate, shared by the unit and
// by a run, so "what you own" is one population.
func repositoriesQuery(spec viewspec.Spec) store.RepositoriesQuery {
	return store.RepositoriesQuery{
		Search: spec.Search, Scope: spec.Scope, Flags: spec.Flags, Grouping: scopeFromSpec(spec),
		CodeScanning: spec.CodeScanning,
	}
}
