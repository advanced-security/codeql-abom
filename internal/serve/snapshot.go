package serve

import (
	"errors"
	"net/http"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// SnapshotPreview is what a submission would do, without doing it.
//
// A submission cannot be withdrawn, so the preview is the only place a target can be inspected
// while the answer is still reversible. Approved reports whether the guard would allow it, which
// is a fact about configuration rather than about the repository, and is stated separately from
// what would be sent.
type SnapshotPreview struct {
	Repository string `json:"repository"`
	// Manifest is the path that would be claimed. It is deliberately not a file GitHub analyzes:
	// claiming one replaces GitHub's own findings for it.
	Manifest string `json:"manifest"`
	// Direct is what GitHub already sees. It is reported to show the submission is not competing
	// with static analysis, and it is never submitted.
	Direct int `json:"direct"`
	// Transitive is what would actually be sent. Zero is a release rather than a no-op: it hands
	// a previously claimed path back to GitHub.
	Transitive  int      `json:"transitive"`
	PackageURLs []string `json:"packageUrls"`
	// Approved reports whether this repository is a configured submission target.
	Approved bool `json:"approved"`
	// Unanalyzed counts the composite dependencies pinned by this repository that have never been
	// analyzed at the pinned ref. A preview reporting few transitive dependencies and a high
	// count here is shallow rather than complete.
	Unanalyzed int `json:"unanalyzed"`
	// Submitted reports whether anything has ever been sent for this repository. Never submitted
	// and submitted nothing are different states wanting different actions.
	Submitted       bool   `json:"submitted"`
	LastSubmittedAt string `json:"lastSubmittedAt,omitempty"`
	// NeedsRenewal is true when what was last sent no longer matches what would be sent now.
	// Snapshots never expire and GitHub re-derives nothing, so a stale submission keeps being
	// reported as fact until it is replaced.
	NeedsRenewal bool `json:"needsRenewal"`
}

// SnapshotResult is what GitHub answered.
type SnapshotResult struct {
	Repository string `json:"repository"`
	Result     string `json:"result"`
	Message    string `json:"message,omitempty"`
	Submitted  int    `json:"submitted"`
	Released   bool   `json:"released"`
}

// notApprovedError is implemented by the guard's refusal so the transport can answer 403 without
// importing the submission package.
type notApprovedError interface{ NotApproved() }

// snapshotPreview reports what would be submitted. It never writes.
func (s *Server) snapshotPreview(w http.ResponseWriter, r *http.Request) {
	if s.SnapshotPreview == nil {
		writeError(w, http.StatusServiceUnavailable,
			"this build cannot preview submissions, so an empty answer here means untried")
		return
	}
	got, err := s.SnapshotPreview(r.PathValue("owner") + "/" + r.PathValue("name"))
	writeJSON(w, got, err)
}

// snapshotSubmit writes to GitHub. It is control-guarded like the other mutating endpoints, and
// the target guard lives below it in the submission client rather than here, so that every path
// to a write passes the same check.
func (s *Server) snapshotSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	if s.SnapshotSubmit == nil {
		writeError(w, http.StatusServiceUnavailable,
			"this build cannot submit snapshots")
		return
	}

	got, err := s.SnapshotSubmit(r.PathValue("owner") + "/" + r.PathValue("name"))
	var refused notApprovedError
	if errors.As(err, &refused) {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeJSON(w, got, err)
}

// submissions lists every snapshot ABOM has written, newest first. It is the only way to find an
// artifact left on a repository, because GitHub offers no way to ask what was submitted.
func (s *Server) submissions(w http.ResponseWriter, r *http.Request) {
	got, err := s.Store.Submissions()
	if got == nil {
		got = []store.Submission{}
	}
	writeJSON(w, got, err)
}
