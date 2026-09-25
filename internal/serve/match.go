package serve

import (
	"net/http"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Match is the answer to "which nodes match", for a caller with no picture to draw.
type Match struct {
	Nodes []store.MatchedNode `json:"nodes"`
	// Total is the node count, which is what a gate decides on: non-empty means fail.
	Total int `json:"total"`
	// Findings counts each finding once however many nodes carry it.
	Findings int `json:"findings"`
	// Unsourced counts what a source term could not answer for: findings with no recorded source.
	Unsourced int `json:"unsourced"`
}

// match answers a set rather than a graph.
//
// Measured 2026-09-10: applied as node filters the finding predicates leave 74% to 99% of their
// own match with no edges, because the graph is bipartite by kind. The Explorer keeps drawing its
// own picture; the gate and the snapshot Action ask here.
func (s *Server) match(w http.ResponseWriter, r *http.Request) {
	spec, err := s.graphPredicate(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsSet(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, unenforceable(refused))
		return
	}
	// The same selector a policy is evaluated through. Two constructions of one predicate is two
	// policy engines, and the one nobody simulated is the one that refuses a pull request.
	matched, err := s.Store.NodesMatching(store.SelectorFor(spec))
	if err != nil {
		// A unit filter with no configured key is the reader asking for something this
		// installation cannot answer, not a fault.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, Match{
		Nodes: matched.Nodes, Total: len(matched.Nodes), Findings: matched.Findings,
		Unsourced: matched.Unsourced,
	}, nil)
}

// unenforceable is the one wording, so a term refused at the endpoint and the same term refused on
// a policy do not read as two different objections.
func unenforceable(terms []string) string {
	return strings.Join(terms, ", ") +
		" cannot narrow a set; a term is enforced or refused, never dropped"
}
