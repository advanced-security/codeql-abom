package serve

import (
	"net/http"

	"github.com/advanced-security/codeql-abom/internal/checks"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// ChecksReport is every check the plan names, each built one's last run, the trend, and the queue.
type ChecksReport struct {
	Checks []CheckState            `json:"checks"`
	Trend  []store.CheckTrendPoint `json:"trend"`
	Queue  store.DisagreementPage  `json:"queue"`
}

// runLogCheck is the check that samples run logs, which workflow_run deliveries say exist.
const runLogCheck = 5

// CheckState is one check and whether this build runs it.
type CheckState struct {
	checks.Check
	// State is `built` or `not built`. A built check that never ran here has no last run.
	State   string          `json:"state"`
	LastRun *store.CheckRun `json:"lastRun"`
	// Frame is the runs check 5 samples from, shown whether or not it is built; no other check has one.
	Frame *store.WorkflowRunFrame `json:"frame,omitempty"`
}

// checks reports what the checker found. Read-only: the checker is the only writer of its tables.
func (s *Server) checks(w http.ResponseWriter, r *http.Request) {
	out := ChecksReport{Checks: make([]CheckState, 0, len(checks.Catalog))}
	latest, err := s.Store.LatestCheckRuns()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	frame, err := s.Store.WorkflowRunFrame()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	for _, c := range checks.Catalog {
		state := CheckState{Check: c, State: "not built"}
		if c.BuiltBy == "" {
			state.State = "built"
			if run, ok := latest[c.Number]; ok {
				state.LastRun = &run
			}
		}
		if c.Number == runLogCheck {
			state.Frame = &frame
		}
		out.Checks = append(out.Checks, state)
	}
	if out.Trend, err = s.Store.CheckTrend(); err != nil {
		writeJSON(w, nil, err)
		return
	}
	out.Queue, err = s.Store.OpenDisagreements(intParam(r, "offset", 0), intParam(r, "top", 50))
	writeJSON(w, out, err)
}
