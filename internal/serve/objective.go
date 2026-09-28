package serve

import (
	"net/http"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Objective is what the crawl is trying to achieve and what it has been spending to get there.
type Objective struct {
	// Progress is completeness at every depth, shallowest first.
	Progress []frontier.DepthProgress `json:"progress"`
	// Working names the depth being worked. HasOutstanding tells a complete closure from work
	// at depth 0.
	Working        int  `json:"working"`
	HasOutstanding bool `json:"hasOutstanding"`
	// MaxDepth is the cap exploration may not pass, so a reader can tell "complete" from
	// "complete as far as we are allowed to look".
	MaxDepth int                `json:"maxDepth"`
	Recent   []store.CycleSpend `json:"recent"`
	Totals   store.CycleTotal   `json:"totals"`
}

func (s *Server) objective(w http.ResponseWriter, r *http.Request) {
	out := Objective{Progress: []frontier.DepthProgress{}, Recent: []store.CycleSpend{}}

	q := &frontier.Queue{Store: s.Store}
	if s.Discovery != nil {
		out.MaxDepth = s.Discovery.Status().MaxDepth
	}
	progress, err := q.Completeness()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out.Progress = progress
	out.Working, out.HasOutstanding = frontier.ShallowestIncomplete(progress)

	if out.Recent, err = s.Store.RecentCycles(intParam(r, "cycles", 20)); err != nil {
		writeJSON(w, nil, err)
		return
	}
	if out.Totals, err = s.Store.CycleTotals(); err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, out, nil)
}
