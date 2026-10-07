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
	MaxDepth int `json:"maxDepth"`
	// StuckAfter is the attempt ceiling each depth's stuck count was taken against.
	StuckAfter int                `json:"stuckAfter"`
	Recent     []store.CycleSpend `json:"recent"`
	Totals     store.CycleTotal   `json:"totals"`
	// Unobserved counts the commits a reference resolves to whose declarations were recorded
	// before ABOM kept each commit's own, which the crawl analyzes again a claim at a time.
	Unobserved int `json:"unobserved"`
	// Stages is the pending work by the stage that claims it, and what the owned closure does
	// not reach.
	Stages frontier.Stages `json:"stages"`
}

func (s *Server) objective(w http.ResponseWriter, r *http.Request) {
	out := Objective{Progress: []frontier.DepthProgress{}, Recent: []store.CycleSpend{}}

	q := &frontier.Queue{Store: s.Store, Closure: s.QueueClosure}
	if s.Discovery != nil {
		st := s.Discovery.Status()
		out.MaxDepth, q.StuckAfter = st.MaxDepth, st.StuckAfter
	}
	out.StuckAfter = q.StuckAfterAttempts()
	progress, err := q.Completeness()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out.Progress = progress
	out.Working, out.HasOutstanding = frontier.ShallowestIncomplete(progress)
	if out.Stages, err = q.Stages(); err != nil {
		writeJSON(w, nil, err)
		return
	}

	if out.Recent, err = s.Store.RecentCycles(intParam(r, "cycles", 20)); err != nil {
		writeJSON(w, nil, err)
		return
	}
	if out.Totals, err = s.Store.CycleTotals(); err != nil {
		writeJSON(w, nil, err)
		return
	}
	inside, err := q.Inside()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	reobservation, err := s.Store.ReobserveInside(0, inside)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out.Unobserved = reobservation.Unobserved
	writeJSON(w, out, nil)
}
