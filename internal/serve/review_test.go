package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// reviewServer serves a pack holding one query whose test has reported something, and a request
// about that query.
func reviewServer(t *testing.T) (*Server, *store.Store, *fakeTester, int64, string) {
	t.Helper()
	s, st := newServer(t)
	root := queryWorkspace(t, []string{"Pin"})
	actual := "| ci.yml:3:9 | actions/checkout is pinned to v4 |\n"
	if err := os.WriteFile(filepath.Join(root, "test", "Pin", "Pin.actual"), []byte(actual), 0o644); err != nil {
		t.Fatal(err)
	}
	tester := &fakeTester{}
	s.Tester, s.QueryDir = tester, root
	id, err := st.SaveQueryRequest(store.QueryRequest{Description: "d", Compliant: "c", NonCompliant: "n",
		RequestedBy: "operator", QueryID: "acme/pin", TargetSource: store.TargetOperator})
	if err != nil {
		t.Fatal(err)
	}
	return s, st, tester, id, root
}

func readReview(t *testing.T, s *Server, id int64) reviewState {
	t.Helper()
	var got reviewState
	if code := sessionGet(t, s, fmt.Sprintf("/api/query-requests/%d/review", id), &got); code != http.StatusOK {
		t.Fatalf("the review answered %d", code)
	}
	return got
}

func review(t *testing.T, s *Server, id int64, verdict, reason, digest string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"verdict": verdict, "reason": reason, "by": "operator",
		"digest": digest})
	if err != nil {
		t.Fatal(err)
	}
	return sessionPost(t, s, fmt.Sprintf("/api/query-requests/%d/review", id), string(body), true)
}

// A person who does not read QL can still read whether the NON_COMPLIANT case was reported and the
// COMPLIANT one was not, so the report is served for the request it answers.
func TestAReviewServesWhatTheQueryReported(t *testing.T) {
	s, _, _, id, _ := reviewServer(t)
	got := readReview(t, s, id)
	if !got.Reviewable || got.QueryID != "acme/pin" || got.QueryVersion == "" || got.Digest == "" ||
		len(got.Tests) != 1 || got.Tests[0].Actual == nil ||
		!strings.Contains(*got.Tests[0].Actual, "pinned to v4") || got.Reviews == nil {
		t.Errorf("review %+v", got)
	}
}

// Approving is what makes a report the expected result, so ABOM accepts exactly what was shown and
// then runs the tests itself: (f) and (g) come from its own invocation, never the agent's.
func TestApprovingAcceptsWhatWasShownAndThenABOMRunsTheTests(t *testing.T) {
	s, st, tester, id, root := reviewServer(t)
	got := readReview(t, s, id)
	if w := sessionPost(t, s, fmt.Sprintf("/api/query-requests/%d/review", id),
		`{"verdict":"approved","by":"operator","digest":"`+got.Digest+`"}`, false); w.Code != http.StatusForbidden {
		t.Errorf("an approval from another origin answered %d, want 403", w.Code)
	}

	w := review(t, s, id, store.ReviewApproved, "", got.Digest)
	if w.Code != http.StatusOK {
		t.Fatalf("approving answered %d: %s", w.Code, w.Body.String())
	}
	if len(tester.accepted) != 1 || len(tester.accepted[0]) != 1 ||
		filepath.Base(tester.accepted[0][0]) != "Pin.qlref" {
		t.Errorf("accepted %v, want the query's own test", tester.accepted)
	}
	expected, err := os.ReadFile(filepath.Join(root, "test", "Pin", "Pin.expected"))
	if err != nil || !strings.Contains(string(expected), "pinned to v4") {
		t.Errorf(".expected is %q %v, want the report that was approved", expected, err)
	}
	tests, err := st.QueryTestsOf("acme/pin")
	if err != nil || len(tester.ran) != 1 || len(tests) != 1 || !tests[0].Passed ||
		tests[0].QueryVersion != versionOf(t, root, "Pin") {
		t.Errorf("ran %v, recorded %+v %v, want ABOM's own passing run of this version", tester.ran, tests, err)
	}
	reviews, err := st.AuthoringReviews(id)
	if err != nil || len(reviews) != 1 || reviews[0].Verdict != store.ReviewApproved ||
		!strings.Contains(reviews[0].Actual, "pinned to v4") || reviews[0].ReviewedBy != "operator" {
		t.Errorf("reviews %+v %v", reviews, err)
	}
	var answered reviewState
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil || !strings.Contains(answered.Tested, "1 of 1 passed") {
		t.Errorf("the approval answered %s %v, want what ABOM's test run said", w.Body.String(), err)
	}
}

// A report listing failed expectations, accepted, makes the test pass by comparing text, without
// the query reporting what its fixtures mark. It can be read and rejected, never accepted.
func TestAReportWhoseExpectationsFailedCannotBeAccepted(t *testing.T) {
	s, st, tester, id, root := reviewServer(t)
	failing := "#select\ntestFailures\n| .github/workflows/ci.yml:6:35:6:43 | # $ Alert | Missing result: Alert |\n"
	if err := os.WriteFile(filepath.Join(root, "test", "Pin", "Pin.actual"), []byte(failing), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readReview(t, s, id)
	if !got.Reviewable || !strings.Contains(got.CannotAccept, "1 marked result") {
		t.Errorf("the review reads reviewable=%v cannotAccept=%q, want it readable and not acceptable",
			got.Reviewable, got.CannotAccept)
	}
	if w := review(t, s, id, store.ReviewApproved, "", got.Digest); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), "1 marked result") {
		t.Errorf("approving answered %d %s, want 409 naming the missing result", w.Code, w.Body.String())
	}
	if len(tester.accepted) != 0 {
		t.Errorf("accepted %v", tester.accepted)
	}
	if w := review(t, s, id, store.ReviewRejected, "the pin on line 6 is not reported", got.Digest); w.Code != http.StatusOK {
		t.Errorf("rejecting answered %d %s", w.Code, w.Body.String())
	}
	if reviews, err := st.AuthoringReviews(id); err != nil || len(reviews) != 1 ||
		reviews[0].Verdict != store.ReviewRejected {
		t.Errorf("reviews %+v %v, want the rejection alone", reviews, err)
	}
}

// What was approved is what was read: a report that changed since is refused, and nothing is accepted.
func TestApprovingAReportThatChangedSinceItWasReadIsRefused(t *testing.T) {
	s, st, tester, id, root := reviewServer(t)
	got := readReview(t, s, id)
	if err := os.WriteFile(filepath.Join(root, "test", "Pin", "Pin.actual"), []byte("| something else |\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if w := review(t, s, id, store.ReviewApproved, "", got.Digest); w.Code != http.StatusConflict {
		t.Errorf("approving a changed report answered %d, want 409", w.Code)
	}
	if reviews, err := st.AuthoringReviews(id); err != nil || len(tester.accepted) != 0 || len(reviews) != 0 {
		t.Errorf("accepted %v and recorded %v %v for a report nobody read", tester.accepted, reviews, err)
	}
}

// A rejection accepts nothing and keeps why, which is what the next session is told.
func TestARejectionKeepsItsReasonAndAcceptsNothing(t *testing.T) {
	s, st, tester, id, _ := reviewServer(t)
	got := readReview(t, s, id)
	if w := review(t, s, id, store.ReviewRejected, " ", got.Digest); w.Code != http.StatusBadRequest {
		t.Errorf("a rejection with no reason answered %d, want 400", w.Code)
	}
	if w := review(t, s, id, store.ReviewRejected, "the COMPLIANT pin is reported", got.Digest); w.Code != http.StatusOK {
		t.Fatalf("rejecting answered %d: %s", w.Code, w.Body.String())
	}
	reviews, err := st.AuthoringReviews(id)
	if err != nil || len(tester.accepted) != 0 || len(tester.ran) != 0 || len(reviews) != 1 ||
		reviews[0].Reason != "the COMPLIANT pin is reported" {
		t.Errorf("accepted %v, ran %v, reviews %+v %v", tester.accepted, tester.ran, reviews, err)
	}
	if after := readReview(t, s, id); len(after.Reviews) != 1 {
		t.Errorf("the review lists %d reviews, want the rejection", len(after.Reviews))
	}
}

// Nothing is decided while an agent is still writing the files the report came from, and an
// approval runs CodeQL, which a crawl under way would contend with.
func TestAReviewWaitsForTheSessionAndAnApprovalForDiscovery(t *testing.T) {
	s, st, tester, id, _ := reviewServer(t)
	got := readReview(t, s, id)
	session, err := st.StartAuthoringSession(id, "operator", "claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	if w := review(t, s, id, store.ReviewRejected, "wrong", got.Digest); w.Code != http.StatusConflict {
		t.Errorf("reviewing while a session works the request answered %d, want 409", w.Code)
	}
	if err := st.EndAuthoringSession(session, store.SessionFinished, "done"); err != nil {
		t.Fatal(err)
	}
	s.Discovery = &crawling{state: supervisor.StateWorking}
	if w := review(t, s, id, store.ReviewApproved, "", got.Digest); w.Code != http.StatusConflict {
		t.Errorf("approving while discovery runs answered %d, want 409", w.Code)
	}
	if len(tester.accepted) != 0 {
		t.Errorf("accepted %v", tester.accepted)
	}
}

// With nothing reported, or a report older than the query, there is nothing an approval could mean.
func TestAReportThatIsMissingOrStaleCannotBeApproved(t *testing.T) {
	s, _, tester, id, root := reviewServer(t)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, "src", "Pin.ql"), future, future); err != nil {
		t.Fatal(err)
	}
	got := readReview(t, s, id)
	if got.Reviewable || !strings.Contains(got.Reason, "query changed") || len(got.Tests) != 1 {
		t.Errorf("a stale report reads %+v, want it shown and not reviewable, saying why", got)
	}
	if w := review(t, s, id, store.ReviewApproved, "", got.Digest); w.Code != http.StatusConflict {
		t.Errorf("approving a stale report answered %d, want 409", w.Code)
	}
	if err := os.Remove(filepath.Join(root, "test", "Pin", "Pin.actual")); err != nil {
		t.Fatal(err)
	}
	if got = readReview(t, s, id); got.Reviewable || got.Reason == "" {
		t.Errorf("no report reads %+v, want it not reviewable, saying why", got)
	}
	if len(tester.accepted) != 0 {
		t.Errorf("accepted %v", tester.accepted)
	}
}
