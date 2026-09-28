package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// reviewState is what an operator reads before deciding: what each of the target's tests last
// reported, and every decision already made about the request's reports.
type reviewState struct {
	QueryID      string             `json:"queryId"`
	QueryVersion string             `json:"queryVersion"`
	Tests        []authoring.Actual `json:"tests"`
	// Digest names the reports as read, and an approval must name it.
	Digest string `json:"digest"`
	// Reviewable is false, with the reason, where there is nothing an approval could mean.
	Reviewable bool   `json:"reviewable"`
	Reason     string `json:"reason"`
	// CannotAccept says why a readable report may be rejected and not accepted, empty when it may be.
	CannotAccept string                  `json:"cannotAccept,omitempty"`
	Reviews      []store.AuthoringReview `json:"reviews"`
	// Tested is what ABOM's own test run said, on the answer to an approval.
	Tested string `json:"tested,omitempty"`
}

type reviewReportRequest struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
	By      string `json:"by"`
	Digest  string `json:"digest"`
}

// requestReview serves what a request's target reported, for an operator to read against the
// request's examples.
func (s *Server) requestReview(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	state, _, err := s.review(id)
	switch {
	case errors.Is(err, store.ErrNoQueryRequest):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, state, nil)
}

// reviewReport records an operator's decision about what a request's target reported. An approval
// makes that report the expected result through the CLI, and then ABOM runs the tests itself, so
// the tests' verdict is never the agent's.
func (s *Server) reviewReport(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	body, ok := decodeReviewReport(w, r)
	if !ok {
		return
	}
	if msg := s.sessionUnderWay(id); msg != "" {
		writeError(w, http.StatusConflict, msg)
		return
	}
	state, q, ok := s.reviewForDecision(w, id, body.Digest)
	if !ok {
		return
	}
	approved := body.Verdict == store.ReviewApproved
	if approved && !s.canAcceptReview(w, state) {
		return
	}
	fixtures, err := authoring.DigestFixtures(s.QueryDir, q.Tests)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.recordReview(id, q, state, body, fixtures); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tested, ok := s.acceptIfApproved(w, r, approved, q, state)
	if !ok {
		return
	}
	after, _, err := s.review(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	after.Tested = tested
	writeJSON(w, after, nil)
}

func decodeReviewReport(w http.ResponseWriter, r *http.Request) (reviewReportRequest, bool) {
	var body reviewReportRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return reviewReportRequest{}, false
	}
	switch {
	case body.Verdict != store.ReviewApproved && body.Verdict != store.ReviewRejected:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("a review is %s or %s, not %q",
			store.ReviewApproved, store.ReviewRejected, body.Verdict))
		return reviewReportRequest{}, false
	case strings.TrimSpace(body.By) == "":
		writeError(w, http.StatusBadRequest, "a review records who made it; sign in with gh")
		return reviewReportRequest{}, false
	case body.Verdict == store.ReviewRejected && strings.TrimSpace(body.Reason) == "":
		writeError(w, http.StatusBadRequest, "a rejection says why, because the next session is told")
		return reviewReportRequest{}, false
	default:
		return body, true
	}
}

func (s *Server) reviewForDecision(w http.ResponseWriter, id int64, digest string) (reviewState, authoring.Query, bool) {
	state, q, err := s.review(id)
	switch {
	case errors.Is(err, store.ErrNoQueryRequest):
		writeError(w, http.StatusNotFound, err.Error())
		return reviewState{}, authoring.Query{}, false
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return reviewState{}, authoring.Query{}, false
	case !state.Reviewable:
		writeError(w, http.StatusConflict, state.Reason)
		return reviewState{}, authoring.Query{}, false
	case digest != state.Digest:
		writeError(w, http.StatusConflict, "what the query reported has changed since it was read; "+
			"read it again before deciding")
		return reviewState{}, authoring.Query{}, false
	default:
		return state, q, true
	}
}

func (s *Server) canAcceptReview(w http.ResponseWriter, state reviewState) bool {
	if state.CannotAccept != "" {
		writeError(w, http.StatusConflict, state.CannotAccept)
		return false
	}
	if code, msg := s.cannotAccept(); msg != "" {
		writeError(w, code, msg)
		return false
	}
	return true
}

func (s *Server) recordReview(
	id int64,
	q authoring.Query,
	state reviewState,
	body reviewReportRequest,
	fixtures string,
) error {
	// Recorded before anything is accepted: an approval nobody can find is an accept done blind.
	_, err := s.Store.RecordAuthoringReview(store.AuthoringReview{
		RequestID: id, QueryID: q.ID, QueryVersion: q.Version, TestDigest: fixtures,
		Actual: reportText(state.Tests), Verdict: body.Verdict, Reason: strings.TrimSpace(body.Reason),
		ReviewedBy: body.By,
	})
	return err
}

func (s *Server) acceptReviewedReport(
	w http.ResponseWriter,
	r *http.Request,
	q authoring.Query,
	state reviewState,
) (string, bool) {
	if err := s.Tester.AcceptTests(r.Context(), reportedTests(q, state.Tests)); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return "", false
	}
	result, code, err := s.ownTests(r.Context(), q)
	if err != nil {
		writeError(w, code, "the report was accepted, and ABOM's own test run then failed: "+err.Error())
		return "", false
	}
	return "ABOM ran the tests: " + result.Detail, true
}

func (s *Server) acceptIfApproved(
	w http.ResponseWriter,
	r *http.Request,
	approved bool,
	q authoring.Query,
	state reviewState,
) (string, bool) {
	if !approved {
		return "", true
	}
	return s.acceptReviewedReport(w, r, q, state)
}

// review reads what a request's target last reported, and the query it came from.
func (s *Server) review(request int64) (reviewState, authoring.Query, error) {
	all, err := s.Store.QueryRequests()
	if err != nil {
		return reviewState{}, authoring.Query{}, err
	}
	i := -1
	for j := range all {
		if all[j].RowID == request {
			i = j
			break
		}
	}
	if i < 0 {
		return reviewState{}, authoring.Query{}, fmt.Errorf("request %d: %w", request, store.ErrNoQueryRequest)
	}
	req := all[i]
	state := reviewState{QueryID: req.QueryID, Tests: []authoring.Actual{}}
	if state.Reviews, err = s.Store.AuthoringReviews(request); err != nil {
		return reviewState{}, authoring.Query{}, err
	}
	switch {
	case req.QueryID == "":
		state.Reason = "no query has been chosen for this request, so nothing has reported anything"
		return state, authoring.Query{}, nil
	case s.QueryDir == "":
		state.Reason = "this server has no query directory; start it with --custom-ql-dir"
		return state, authoring.Query{}, nil
	}
	q, found, err := authoring.Find(s.QueryDir, req.QueryID)
	switch {
	case err != nil:
		return reviewState{}, authoring.Query{}, err
	case !found:
		state.Reason = req.QueryID + " is not a query in the query directory"
		return state, authoring.Query{}, nil
	}
	state.QueryVersion = q.Version
	if state.Tests, err = authoring.Actuals(s.QueryDir, q); err != nil {
		return reviewState{}, authoring.Query{}, err
	}
	state.Digest = authoring.DigestActuals(state.Tests)
	state.Reviewable, state.Reason = reviewable(q, state.Tests)
	state.CannotAccept = unmet(state.Tests)
	return state, q, nil
}

// unmet says why a report cannot become the expected result: accepted, a report listing failed
// expectations makes the test pass by comparing text, without the query reporting what it marks.
func unmet(tests []authoring.Actual) string {
	for _, t := range tests {
		if t.Actual == nil {
			continue
		}
		missing, unexpected := runner.ExpectationFailures([]byte(*t.Actual))
		if missing+unexpected == 0 {
			continue
		}
		var parts []string
		if missing > 0 {
			parts = append(parts, fmt.Sprintf("misses %d marked %s", missing, plural(missing, "result", "results")))
		}
		if unexpected > 0 {
			parts = append(parts, fmt.Sprintf("reports %d %s nothing marks", unexpected,
				plural(unexpected, "result", "results")))
		}
		return fmt.Sprintf("%s %s, and accepting it would make the test pass without the query doing "+
			"what its fixtures mark; reject it, saying what should change", t.Test, strings.Join(parts, " and "))
	}
	return ""
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func reviewable(q authoring.Query, tests []authoring.Actual) (bool, string) {
	if len(tests) == 0 {
		return false, "no test names " + q.Rel + ", so there is no report to read"
	}
	reported := false
	for _, t := range tests {
		if t.Stale != "" {
			return false, t.Test + ": " + t.Stale
		}
		reported = reported || t.Actual != nil
	}
	if !reported {
		return false, "the tests have reported nothing to read; run them, or let a session run them"
	}
	return true, ""
}

// sessionUnderWay says why nothing is decided while an agent still writes what the report came from.
func (s *Server) sessionUnderWay(request int64) string {
	sessions, err := s.Store.AuthoringSessions(request)
	if err != nil {
		return err.Error()
	}
	for _, a := range sessions {
		if a.State == store.SessionRunning {
			return fmt.Sprintf("session %d is working this request; decide on what it reported once it ends",
				a.RowID)
		}
	}
	return ""
}

// cannotAccept says why an approval cannot run CodeQL here now, or is empty when it can.
func (s *Server) cannotAccept() (int, string) {
	if s.Tester == nil {
		return http.StatusServiceUnavailable, "this server cannot run CodeQL tests; start it with --custom-ql-dir"
	}
	if msg := s.discoveryHoldsCodeQL("accepting"); msg != "" {
		return http.StatusConflict, msg
	}
	return 0, ""
}

// discoveryHoldsCodeQL says why an action that runs CodeQL must wait for the crawl, or is empty.
func (s *Server) discoveryHoldsCodeQL(action string) string {
	if s.Discovery == nil {
		return ""
	}
	if state := s.Discovery.Status().State; state != supervisor.StatePaused && state != supervisor.StateStopped {
		return fmt.Sprintf("discovery is %s, and %s runs CodeQL; pause discovery first", state, action)
	}
	return ""
}

// reportedTests are the tests that reported something: a test with no report has nothing to accept.
func reportedTests(q authoring.Query, actuals []authoring.Actual) []string {
	out := []string{}
	for i, a := range actuals {
		if a.Actual != nil && i < len(q.Tests) {
			out = append(out, q.Tests[i])
		}
	}
	return out
}

// reportText is the report as the reviewer read it, test by test where there is more than one.
func reportText(actuals []authoring.Actual) string {
	reported := []authoring.Actual{}
	for _, a := range actuals {
		if a.Actual != nil {
			reported = append(reported, a)
		}
	}
	if len(reported) == 1 {
		return *reported[0].Actual
	}
	var out strings.Builder
	for _, a := range reported {
		fmt.Fprintf(&out, "== %s ==\n%s", a.Test, *a.Actual)
	}
	return out.String()
}
