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
	// ProducedBy is what drew this: "graphviz", or "none" when the layout could not be computed
	// and whoever asked has to fall back. A caller that could not tell them apart would present a
	// force simulation's picture as a stable one.
	ProducedBy string `json:"producedBy"`
	Engine     string `json:"engine"`
	// Reason says why nothing was produced, so the interface can report the cause rather than
	// silently drawing something else.
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
		// Not a fault. Graphviz is optional, and the honest answer is that nothing placed this
		// -- said out loud, so the interface reports which layout a reader is looking at.
		writeJSON(w, GraphLayout{
			ProducedBy: "none",
			Engine:     string(engine),
			Reason:     err.Error(),
			Nodes:      len(g.Nodes),
			Positions:  []layout.Position{},
			Clusters:   []layout.Cluster{},
			Unplaced:   len(g.Nodes),
		}, nil)
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
