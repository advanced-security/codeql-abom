package serve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/figure"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/layout"
	"github.com/advanced-security/codeql-abom/internal/store"
	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// NodeFigure is a details page's figure: what runs its node and what it runs, drawn.
type NodeFigure struct {
	Key    string        `json:"key"`
	SVG    string        `json:"svg"`
	Width  float64       `json:"width"`
	Height float64       `json:"height"`
	Counts figure.Counts `json:"counts"`
	// Terms are the page's terms the figure was cut under, as a reader reads them.
	Terms []string `json:"terms"`
	// Caption is what the page says under the heading. The drawing's description repeats it;
	// the drawing does not paint it.
	Caption   []string `json:"caption"`
	Direction string   `json:"direction"`
	Graphviz  string   `json:"graphviz"`
}

// figureFormatting are the parameters that say how to draw rather than what to draw.
var figureFormatting = []string{"direction"}

// allCallers asks the uses walk for every row at once: a figure draws from all of them.
const allCallers = 1 << 30

// nodeFigure cuts the closure of one node from the Explorer's projection and draws it.
//
// What runs it is what the page's own table lists, under the same terms; what it runs is drawn
// whole, as the page lists it, at the versions `at` names.
func (s *Server) nodeFigure(w http.ResponseWriter, r *http.Request, key string, standalone bool) {
	spec, err := s.graphPredicate(r, figureFormatting...)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsUses(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, unlistable(refused))
		return
	}
	direction := figure.Direction(r.URL.Query().Get("direction"))
	if direction == "" {
		direction = figure.LeftToRight
	}
	if !slices.Contains(figure.Directions(), direction) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is not a direction a figure is drawn in; use %v",
			direction, figure.Directions()))
		return
	}
	answer, err := s.drawFigure(r.Context(), key, spec.String(), direction, func() (figure.Closure, []string, error) {
		return s.cutFigure(key, spec)
	})
	switch {
	case layout.ErrUnavailable(err):
		writeGraphvizMissing(w, err)
		return
	case errors.Is(err, figure.ErrNothingToDraw):
		writeError(w, http.StatusNotFound, fmt.Sprintf("nothing here is named %s, so there is nothing to draw", key))
		return
	case err != nil:
		writeJSON(w, nil, err)
		return
	}
	if standalone {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", figureFilename(key)))
		_, _ = w.Write([]byte(answer.SVG))
		return
	}
	writeJSON(w, answer, nil)
}

// cutFigure takes the closure under the page's terms: callers where the uses table's rows start.
func (s *Server) cutFigure(key string, spec viewspec.Spec) (figure.Closure, []string, error) {
	g, err := s.Store.FullGraph(store.FullGraphOptions{})
	if err != nil {
		return figure.Closure{}, nil, err
	}
	q, err := s.usesNarrowing(spec)
	if err != nil {
		return figure.Closure{}, nil, err
	}
	q.Limit = allCallers
	uses, err := (&graph.Builder{Store: s.Store, Models: &s.models}).UsesOf(key, q)
	if err != nil {
		return figure.Closure{}, nil, err
	}
	callers := map[string]bool{}
	for _, u := range uses.Items {
		for _, start := range u.Workflows {
			callers[start] = true
		}
	}
	return figure.Cut(g, key, figure.CutOptions{At: spec.At, Callers: callers}), spec.TermPhrases(), nil
}

// drawFigure answers from the cache when the graph and Graphviz are those it was drawn from.
func (s *Server) drawFigure(ctx context.Context, key, predicate string, direction figure.Direction,
	cut func() (figure.Closure, []string, error)) (NodeFigure, error) {
	graphviz, err := s.graphvizVersion(ctx)
	if err != nil {
		return NodeFigure{}, err
	}
	version, err := s.Store.GraphVersion()
	if err != nil {
		return NodeFigure{}, err
	}
	cacheKey := strings.Join([]string{key, predicate, string(direction), fmt.Sprint(version.Generation),
		version.UpdatedAt, graphviz}, "\x00")
	if answer, ok := s.figures.get(cacheKey); ok {
		return answer, nil
	}
	c, terms, err := cut()
	if err != nil {
		return NodeFigure{}, err
	}
	meta := figure.Meta{Terms: terms, AsOf: asOf(version.UpdatedAt), Graphviz: graphviz}
	f, err := figure.Draw(ctx, c, figure.Options{Direction: direction}, meta)
	if err != nil {
		return NodeFigure{}, err
	}
	answer := NodeFigure{Key: key, SVG: string(f.SVG), Width: f.Width, Height: f.Height, Counts: c.Counts,
		Terms: terms, Caption: figure.Caption(c, meta), Direction: string(direction), Graphviz: graphviz}
	if answer.Terms == nil {
		answer.Terms = []string{}
	}
	if answer.Caption == nil {
		answer.Caption = []string{}
	}
	s.figures.put(cacheKey, answer)
	return answer, nil
}

// graphvizVersion is the version serve started with, or the installed one for a server nobody
// started through serve.
func (s *Server) graphvizVersion(ctx context.Context) (string, error) {
	if s.Graphviz != "" {
		return s.Graphviz, nil
	}
	return layout.Version(ctx)
}

// asOf words when the graph was recorded, to the minute: the moment a figure was cut from.
func asOf(updatedAt string) string {
	at, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return updatedAt
	}
	return at.UTC().Format("2006-01-02 15:04 UTC")
}

// figureFilename is a download's name, from the key in characters every file system accepts.
func figureFilename(key string) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' {
			return r
		}
		return '_'
	}, key)
	return "abom-" + strings.Trim(name, "_.") + ".svg"
}

// figureCache keeps the most recent figures. A figure is a few tens of kilobytes, and one page
// asks for one at a time.
type figureCache struct {
	mu      sync.Mutex
	entries map[string]NodeFigure
	order   []string
}

const figureCacheSize = 256

func (c *figureCache) get(key string) (NodeFigure, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.entries[key]
	return f, ok
}

func (c *figureCache) put(key string, f NodeFigure) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]NodeFigure{}
	}
	if _, ok := c.entries[key]; !ok {
		c.order = append(c.order, key)
	}
	c.entries[key] = f
	for len(c.order) > figureCacheSize {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
}
