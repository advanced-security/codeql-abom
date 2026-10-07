package serve

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/graphexport"
	"github.com/advanced-security/codeql-abom/internal/layout"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// GraphLayout is where an engine placed a scope, and what it could not place.
type GraphLayout struct {
	// ProducedBy is what drew this, always "graphviz": a server that cannot run it answers 503.
	ProducedBy string `json:"producedBy"`
	Engine     string `json:"engine"`
	// Reason says why a grouping that was asked for draws no boundary.
	Reason    string            `json:"reason,omitempty"`
	Width     float64           `json:"width"`
	Height    float64           `json:"height"`
	Nodes     int               `json:"nodes"`
	Positions []layout.Position `json:"positions"`
	Clusters  []layout.Cluster  `json:"clusters"`
	// GroupingDrawn is false when a grouping was asked for and this engine does not lay clusters
	// out. Saying nothing would leave a reader who grouped and saw no boundaries concluding the
	// corpus has none.
	GroupingDrawn bool `json:"groupingDrawn"`
	// Unplaced counts nodes the engine returned no position for. They are not an error and they
	// are not zero-placed: whoever draws this has to know it is holding a partial answer.
	Unplaced int `json:"unplaced"`
}

// layoutFormatting are the parameters that say how to lay out rather than what to lay out.
var layoutFormatting = []string{"engine", "groupBy"}

// graphLayout places a scope with Graphviz and hands back coordinates.
//
// It answers from the same predicate as `/api/graph/full` and encodes the same projection the
// export does, so the picture, the file and the listing are three renderings of one scope.
func (s *Server) graphLayout(w http.ResponseWriter, r *http.Request) {
	spec, err := s.graphPredicate(r, layoutFormatting...)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsGraph(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, undrawable(refused))
		return
	}

	groupBy := r.URL.Query().Get("groupBy")
	if groupBy != "" && !graphGroupings[groupBy] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"%q is not a grouping; use owner, repository or workflow", groupBy))
		return
	}
	engine := layout.Engine(strings.ToLower(r.URL.Query().Get("engine")))
	if engine == "" {
		engine = layout.Default
	}

	opts, err := s.graphOptions(spec)
	if err != nil {
		writeJSON(w, GraphLayout{}, err)
		return
	}
	g, err := s.Store.FullGraph(opts)
	if err != nil {
		writeJSON(w, GraphLayout{}, err)
		return
	}
	g.ClientApplied = spec.ClientApplied()

	document, err := graphexport.DOT(g, graphexport.Options{
		Predicate: predicateText(r.URL.Query()),
		GroupBy:   groupBy,
	})
	if err != nil {
		writeJSON(w, GraphLayout{}, err)
		return
	}

	result, err := layout.Compute(r.Context(), document, layout.Options{Engine: engine})
	if layout.ErrUnavailable(err) {
		writeGraphvizMissing(w, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	response := GraphLayout{
		ProducedBy:    "graphviz",
		Engine:        result.Engine,
		Width:         result.Width,
		Height:        result.Height,
		Nodes:         len(g.Nodes),
		Positions:     placed(result.Positions),
		Clusters:      placed(result.Clusters),
		GroupingDrawn: groupBy == "" || layout.SupportsClusters(engine),
		Unplaced:      unplaced(g, result),
	}
	if groupBy != "" && !response.GroupingDrawn {
		response.Reason = fmt.Sprintf(
			"%s places nodes but draws no cluster boundary; group with dot, neato, fdp or osage",
			engine)
	}
	writeJSON(w, response, nil)
}

// unplaced counts what the engine did not return a position for.
//
// A node drawn at the origin because nothing placed it is indistinguishable from one the engine
// genuinely put there, which is the same absent-measurement confusion the graph refuses elsewhere.
//
// Counted by name rather than by subtracting totals: the DOT export writes container nodes the
// graph payload does not carry, so the two counts are of different sets and their difference hides
// a real gap behind padding.
func unplaced(g store.Graph, result layout.Result) int {
	at := make(map[string]struct{}, len(result.Positions))
	for _, p := range result.Positions {
		at[p.Key] = struct{}{}
	}
	missing := 0
	for _, n := range g.Nodes {
		if _, ok := at[n.Key]; !ok {
			missing++
		}
	}
	return missing
}

// placed keeps a nil slice out of the payload, so a caller reads an empty list rather than null.
func placed[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}

// writeGraphvizMissing answers for a server that has lost Graphviz, which is a broken installation:
// 503 and the remedy, never a placement nobody made.
func writeGraphvizMissing(w http.ResponseWriter, err error) {
	writeError(w, http.StatusServiceUnavailable,
		fmt.Sprintf("this server cannot run Graphviz (%v); %s", err, layout.Remedy))
}
