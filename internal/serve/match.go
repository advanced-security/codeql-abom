package serve

import (
	"net/http"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// Match is the answer to "which nodes match", for a caller with no picture to draw.
type Match struct {
	Nodes []store.MatchedNode `json:"nodes"`
	// Total is the node count, which is what a gate decides on: non-empty means fail.
	Total int `json:"total"`
	// Findings counts each finding once however many nodes carry it.
	Findings int `json:"findings"`
}

// graphOnlyParams are predicates that shape a graph and mean nothing to a set.
//
// Refused rather than ignored. Four predicates in this language have already been accepted and
// silently dropped — `origins`, `minReach`, `flags` and `ref` — and each time the corpus, not a
// test, is what noticed. A caller who asks this endpoint for `kinds=workflow` and is handed every
// kind has no way to tell.
var graphOnlyParams = []string{"kinds", "edges", "boundary", "minReach", "origins", "ref", "steps"}

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
	if refused := refusedForMatching(r); len(refused) > 0 {
		writeError(w, http.StatusBadRequest,
			"view: "+strings.Join(refused, ", ")+" shape a graph and cannot narrow a set")
		return
	}
	matched, err := s.Store.NodesMatching(store.FindingSelector{
		Rules:        spec.Rules,
		Exclude:      spec.NotRules,
		Provider:     spec.Provider,
		Severity:     spec.Severity,
		Repositories: spec.Repositories,
	})
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, Match{
		Nodes: matched.Nodes, Total: len(matched.Nodes), Findings: matched.Findings,
	}, nil)
}

// refusedForMatching names the graph-shaping parameters a request carried, resolving a named view
// first so a view holding one is refused for the same reason a literal one is.
func refusedForMatching(r *http.Request) []string {
	q := r.URL.Query()
	present := func(key string) bool { return q.Get(key) != "" }
	if name := q.Get(viewNameParam); name != "" {
		b, ok := viewspec.BuiltinNamed(name)
		if !ok {
			return nil
		}
		inView, err := viewspec.Parse(b.Predicate)
		if err != nil {
			return nil
		}
		present = func(key string) bool {
			return strings.Contains(inView.String(), key+"=")
		}
	}
	var refused []string
	for _, key := range graphOnlyParams {
		if present(key) {
			refused = append(refused, key)
		}
	}
	return refused
}
