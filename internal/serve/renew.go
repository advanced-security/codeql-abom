package serve

import "net/http"

// RenewalReport is what a renewal pass did, named repository by repository.
//
// A count is not an audit trail. A submission cannot be withdrawn and GitHub offers no way to ask
// what has been submitted, so the only record of a write is the one made here.
type RenewalReport struct {
	// Checked is every approved target considered, so a short report reads as a small allow-list
	// rather than a pass that gave up early.
	Checked int `json:"checked"`
	// Renewed names the repositories actually written to.
	Renewed []RenewalOutcome `json:"renewed"`
	// UpToDate is checked and unchanged. Re-sending an identical snapshot achieves nothing and
	// makes the ledger unreadable.
	UpToDate []string `json:"upToDate"`
	// Skipped is approved but never submitted to. A first submission is a deliberate act, not
	// something a batch pass performs, or adding a name to the allow-list writes to it at once.
	Skipped []string         `json:"skipped"`
	Failed  []RenewalFailure `json:"failed"`
}

type RenewalOutcome struct {
	Repository string `json:"repository"`
	Packages   int    `json:"packages"`
	Released   bool   `json:"released"`
}

type RenewalFailure struct {
	Repository string `json:"repository"`
	Error      string `json:"error"`
}

// renew re-sends the approved targets whose supply chain no longer matches what was submitted.
//
// It writes to repositories the operator has already named in the allow-list, and only to those
// already submitted to, so it introduces no target that had not been consented to. What it does
// not do is decide *when* to run: that is a human action, because a process that writes to other
// people's repositories on a timer is a different decision from one that does it when asked.
func (s *Server) renew(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	if s.ApprovedTargets == nil || s.SnapshotPreview == nil || s.SnapshotSubmit == nil {
		writeError(w, http.StatusServiceUnavailable,
			"this build cannot submit snapshots, so an empty report here would mean untried "+
				"rather than nothing to renew")
		return
	}

	report := RenewalReport{
		Renewed:  []RenewalOutcome{},
		UpToDate: []string{},
		Skipped:  []string{},
		Failed:   []RenewalFailure{},
	}
	for _, repository := range s.ApprovedTargets() {
		report.Checked++

		state, err := s.SnapshotPreview(repository)
		if err != nil {
			report.Failed = append(report.Failed,
				RenewalFailure{Repository: repository, Error: err.Error()})
			continue
		}
		if !state.Submitted {
			report.Skipped = append(report.Skipped, repository)
			continue
		}
		if !state.NeedsRenewal {
			report.UpToDate = append(report.UpToDate, repository)
			continue
		}

		// One unreachable repository must not discard the others: this is several independent
		// writes and a partial answer is still worth keeping.
		res, err := s.SnapshotSubmit(repository)
		if err != nil {
			report.Failed = append(report.Failed,
				RenewalFailure{Repository: repository, Error: err.Error()})
			continue
		}
		report.Renewed = append(report.Renewed, RenewalOutcome{
			Repository: repository, Packages: res.Submitted, Released: res.Released,
		})
	}
	writeJSON(w, report, nil)
}
