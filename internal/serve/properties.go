package serve

import (
	"net/http"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// propertiesView is how one custom property key divides the repositories the operator owns.
//
// The key is named in the answer rather than assumed by the caller: a reader shown "7 repositories
// in 3" and not told which property that came from has no way to check it against the
// organization, and checking it is the whole point of reading the line rather than inferring it.
type propertiesView struct {
	// Configured is the key the operator named, empty when they named none. Nothing is guessed
	// from whatever the corpus happens to hold, which would be the inference this avoids.
	Configured string `json:"configured"`
	// Keys are the property names the corpus has seen a value for, so an operator choosing one
	// is shown what the organization administers rather than asked to remember it.
	Keys      []string                `json:"keys"`
	Breakdown store.PropertyBreakdown `json:"breakdown"`
}

// properties serves the breakdown for the configured key, or for one named in the query.
func (s *Server) properties(w http.ResponseWriter, r *http.Request) {
	keys, err := s.Store.PropertyKeys()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out := propertiesView{Configured: s.GroupProperty, Keys: keys}

	name := r.URL.Query().Get("property")
	if name == "" {
		name = s.GroupProperty
	}
	// No key means no dividing line. An empty breakdown is the honest answer; picking one of the
	// keys on the operator's behalf would put repositories in units nobody chose.
	if name == "" {
		out.Breakdown.Values = []store.PropertyCount{}
		writeJSON(w, out, nil)
		return
	}

	breakdown, err := s.Store.PropertyBreakdown(name)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out.Breakdown = breakdown
	writeJSON(w, out, nil)
}
