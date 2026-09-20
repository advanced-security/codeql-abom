package serve

import (
	"net/http"

	"github.com/advanced-security/codeql-abom/internal/enumerate"
)

// The enumeration report and its parts live in internal/enumerate, so that a person clicking a
// button and a loop running on a timer produce the same answer from the same code. Aliased rather
// than re-declared: the JSON these serialize to is what the interface reads.
type (
	EnumerationReport  = enumerate.Report
	AccountEnumeration = enumerate.AccountEnumeration
	AccountFailure     = enumerate.AccountFailure
)

// enumerate turns the declared scope into repositories.
//
// It writes the corpus and spends the operator's rate limit, so it is control-guarded like the
// rest, and it takes no request body: the declaration is the input, and accepting a list here
// would let a caller crawl accounts nobody declared.
func (s *Server) enumerate(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	if s.Enumerate == nil {
		writeError(w, http.StatusServiceUnavailable,
			"this build cannot enumerate accounts, so an empty result here means untried, not empty")
		return
	}

	report, err := enumerate.Run(r.Context(), enumerate.Options{
		Store:            s.Store,
		Read:             s.Enumerate,
		Schema:           s.EnumerateSchema,
		QueueOwned:       s.QueueOwned,
		FullListingEvery: s.FullListingEvery,
	})
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, report, nil)
}
