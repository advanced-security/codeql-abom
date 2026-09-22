package serve

import (
	"net/http"

	"github.com/advanced-security/codeql-abom/internal/store"
	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// filters serves what a reader can narrow by.
//
// Answered from the language rather than from the corpus, so it needs no store and holds whether
// or not anything has been crawled: a filter list that waits on a crawl reads as an interface that
// is broken rather than one that is empty. Which *values* a term has is a separate question, and
// each term names the endpoint that answers it.
func (s *Server) filters(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, viewspec.Catalog(), nil)
}

// filterCounts reports how many values each term has, so a control can say so before a reader
// opens it. Nineteen terms is too many to open one at a time to find the four worth using.
//
// A term is present with a number or absent entirely. Zero means the corpus holds none of that
// value, which is a measurement; absent means nobody counted, which is not. Reporting the second
// as the first would rank a working filter down for a number it never got.
func (s *Server) filterCounts(w http.ResponseWriter, _ *http.Request) {
	counts, err := s.Store.FilterValueCounts()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	// The language's own counts win: a declared vocabulary is the values the term allows, not the
	// values something in this corpus happens to have used.
	for _, term := range viewspec.Catalog() {
		if declared := viewspec.Vocabulary(term.Name); len(declared) > 0 {
			counts[term.Name] = len(declared)
		}
	}
	writeJSON(w, counts, nil)
}

// filterValues serves the values one term can take, so a control can offer them rather than
// asking a reader to type an identifier from memory.
//
// Answered from the language where the language declares one, and from the corpus otherwise. Both
// are lists of the same shape on purpose: which of the two a term uses is the catalog's business,
// not the caller's, and a control that had to know would be a second declaration of it.
func (s *Server) filterValues(w http.ResponseWriter, r *http.Request) {
	term := r.PathValue("term")

	if declared := viewspec.Vocabulary(term); len(declared) > 0 {
		// No counts: these are the values the term allows, not the values something happens to
		// have used, and a zero beside one would read as "this matches nothing" rather than
		// "nobody has looked". Nothing is left out either, so there is nothing it cannot speak for.
		out := make([]store.FilterValue, 0, len(declared))
		for _, v := range declared {
			out = append(out, store.FilterValue{Value: v})
		}
		writeJSON(w, store.FilterValueSet{Values: out}, nil)
		return
	}

	set, err := s.Store.FilterValues(term)
	if err != nil {
		// Named rather than answered empty: an empty list reads as "the corpus contains none of
		// those", which is a measurement nobody took.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, set, nil)
}
