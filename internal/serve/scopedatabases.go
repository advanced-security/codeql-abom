package serve

import (
	"net/http"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// databases answers which databases a scope would have to be run against, and which it cannot.
//
// The same predicate, refusal and selector as /api/match, deliberately: one language asked in two
// directions. It builds nothing and schedules nothing, so a reader can see the size of a thing
// before deciding whether it is worth doing.
func (s *Server) databases(w http.ResponseWriter, r *http.Request) {
	spec, err := s.graphPredicate(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsSet(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, unenforceable(refused))
		return
	}
	set, err := s.Store.DatabasesMatching(store.SelectorFor(spec))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, set, nil)
}
