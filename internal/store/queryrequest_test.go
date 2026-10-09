package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func aRequest() QueryRequest {
	return QueryRequest{
		Description:  "a self-hosted runner reached from a pull_request trigger",
		Compliant:    "runs-on: ubuntu-latest",
		NonCompliant: "runs-on: self-hosted",
		RequestedBy:  "operator",
	}
}

// Without both examples a request has nothing to turn into a test.
func TestAQueryRequestNeedsADescriptionAndBothExamples(t *testing.T) {
	s := newTestStore(t)
	for name, blank := range map[string]func(*QueryRequest){
		"description":   func(r *QueryRequest) { r.Description = "  " },
		"compliant":     func(r *QueryRequest) { r.Compliant = "" },
		"non-compliant": func(r *QueryRequest) { r.NonCompliant = "\n" },
		"requester":     func(r *QueryRequest) { r.RequestedBy = "" },
	} {
		r := aRequest()
		blank(&r)
		if _, err := s.SaveQueryRequest(r); err == nil {
			t.Errorf("a request with no %s was kept", name)
		}
	}
	if got, err := s.QueryRequests(); err != nil || len(got) != 0 {
		t.Errorf("refused requests left %d rows (err %v)", len(got), err)
	}
}

func TestAQueryRequestIsKeptOpenAndSaysWhoAsked(t *testing.T) {
	s := newTestStore(t)
	id, err := s.SaveQueryRequest(aRequest())
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d requests, want 1", len(got))
	}
	r := got[0]
	if r.RowID != id || r.Status != RequestOpen || r.RequestedBy != "operator" || r.CreatedAt == "" {
		t.Errorf("kept %+v", r)
	}
	if r.NonCompliant != "runs-on: self-hosted" {
		t.Errorf("the non-compliant example came back as %q", r.NonCompliant)
	}
}

// An edit changes what is asked for, never who asked or when: the request is theirs.
func TestEditingAQueryRequestKeepsWhoAskedAndWhen(t *testing.T) {
	s := newTestStore(t)
	id, err := s.SaveQueryRequest(aRequest())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.QueryRequests()

	edit := aRequest()
	edit.RowID = id
	edit.RequestedBy = "somebody-else"
	edit.Status = RequestInProgress
	edit.Kind = RequestMissed
	edit.QueryID = "acme/self-hosted-runner"
	edit.TargetSource = TargetOperator
	if _, err := s.SaveQueryRequest(edit); err != nil {
		t.Fatal(err)
	}

	after, _ := s.QueryRequests()
	if len(after) != 1 {
		t.Fatalf("an edit left %d requests", len(after))
	}
	r := after[0]
	if r.RequestedBy != "operator" || r.CreatedAt != before[0].CreatedAt {
		t.Errorf("an edit rewrote the requester or the date: %+v", r)
	}
	if r.Status != RequestInProgress || r.Kind != RequestMissed || r.QueryID != "acme/self-hosted-runner" {
		t.Errorf("the edit was not kept: %+v", r)
	}
}

// A request keeps the steps it was first closed on through later edits, and forgets them when it
// is reopened; one that is not closed, or was closed before steps were kept, has none.
func TestAClosedRequestKeepsTheStepsItFirstClosedOn(t *testing.T) {
	s := newTestStore(t)
	r := aRequest()
	r.ClosedSteps = `["open"]`
	id, err := s.SaveQueryRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	r.RowID = id
	read := func() string {
		t.Helper()
		got, err := s.QueryRequests()
		if err != nil || len(got) != 1 {
			t.Fatalf("read %v, %v", got, err)
		}
		return got[0].ClosedSteps
	}
	for _, c := range []struct{ status, steps, want string }{
		{RequestOpen, `["open"]`, ""},
		{RequestDeclined, "", ""},
		{RequestOpen, "", ""},
		{RequestDeclined, `["first"]`, `["first"]`},
		{RequestDone, `["second"]`, `["first"]`},
		{RequestInProgress, `["third"]`, ""},
		{RequestDone, `["fourth"]`, `["fourth"]`},
	} {
		r.Status, r.ClosedSteps = c.status, c.steps
		if _, err := s.SaveQueryRequest(r); err != nil {
			t.Fatal(err)
		}
		if got := read(); got != c.want {
			t.Errorf("marked %s offering %q, it keeps %q, want %q", c.status, c.steps, got, c.want)
		}
	}
}

func TestAQueryRequestRefusesAValueOutsideItsVocabulary(t *testing.T) {
	s := newTestStore(t)
	r := aRequest()
	r.Status = "closed"
	if _, err := s.SaveQueryRequest(r); err == nil || !strings.Contains(err.Error(), RequestDeclined) {
		t.Errorf("an unknown status was not refused with the alternatives: %v", err)
	}
	r = aRequest()
	r.Kind = "bug"
	if _, err := s.SaveQueryRequest(r); err == nil {
		t.Error("an unknown kind was kept")
	}
}

func TestEditingARequestThatDoesNotExistIsRefused(t *testing.T) {
	s := newTestStore(t)
	r := aRequest()
	r.RowID = 41
	if _, err := s.SaveQueryRequest(r); err == nil {
		t.Error("an edit of a request nobody made was accepted")
	}
}

func TestNoQueryRequestsIsAnEmptyListNotNull(t *testing.T) {
	s := newTestStore(t)
	got, err := s.QueryRequests()
	if err != nil || got == nil {
		t.Errorf("got %v, %v; want an empty list", got, err)
	}
}

// "Selected by you" and "decided by the agent" are different claims about one target, and a
// target nobody is recorded as choosing is neither.
func TestARequestsTargetSaysWhoChoseIt(t *testing.T) {
	s := newTestStore(t)
	r := aRequest()
	r.QueryID, r.TargetSource = "acme/self-hosted-runner", TargetOperator
	if _, err := s.SaveQueryRequest(r); err != nil {
		t.Fatal(err)
	}
	got, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].TargetSource != TargetOperator {
		t.Errorf("read back chosen by %q", got[0].TargetSource)
	}

	for name, change := range map[string]func(*QueryRequest){
		"a target nobody chose":    func(r *QueryRequest) { r.QueryID = "acme/x" },
		"a chooser with no target": func(r *QueryRequest) { r.TargetSource = TargetOperator },
		"a chooser outside the vocabulary": func(r *QueryRequest) {
			r.QueryID, r.TargetSource = "acme/x", "somebody"
		},
	} {
		r := aRequest()
		change(&r)
		if _, err := s.SaveQueryRequest(r); err == nil {
			t.Errorf("%s was kept", name)
		}
	}
}

// A request kept before its target's chooser was recorded reads as not recorded, not as chosen by
// the operator.
func TestARequestKeptBeforeTargetsWereAttributedSaysNobodyIsRecorded(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.db.Exec(`
CREATE TABLE abom_query_request (
  row_id        INTEGER PRIMARY KEY,
  description   TEXT NOT NULL,
  compliant     TEXT NOT NULL,
  non_compliant TEXT NOT NULL,
  query_id      TEXT NOT NULL DEFAULT '',
  kind          TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL,
  requested_by  TEXT NOT NULL,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
INSERT INTO abom_query_request VALUES (1, 'd', 'c', 'n', 'acme/old', '', 'open', 'operator',
  '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	got, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].QueryID != "acme/old" || got[0].TargetSource != "" {
		t.Errorf("an old request reads %+v", got)
	}
}

// pickedRun is a run of acme/a over three repositories: one where it fired, one where it ran and
// found nothing, and one nothing ran against.
func pickedRun(t *testing.T, s *Store) (runID, fired int64) {
	t.Helper()
	repo, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	held := func(name string) ScopedDatabase {
		return ScopedDatabase{DatabaseIdentity: DatabaseIdentity{
			Repository: name, Commit: "c1", Mode: AnalysisModeRegular,
		}, Status: DatabaseRetained, Path: "/db/" + name}
	}
	runID, err = s.CreateAnalysisRun(AnalysisRequest{
		RequestedBy: "operator", Queries: []AnalysisQuery{{QueryID: "acme/a", QueryVersion: "v1"}},
		Databases: []ScopedDatabase{held("octo/app"), held("octo/quiet")},
		Gaps:      []DatabaseGap{{Repository: "octo/absent", Reason: "never analyzed"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"octo/app", "octo/quiet"} {
		qr, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular, QueryID: "acme/a",
			QueryVersion: "v1", Repository: name, DBCommit: "c1"})
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		if name == "octo/app" {
			if _, err := s.AddFindings([]Finding{{QueryRunID: qr, RuleID: "acme/a",
				RepositoryID: repo, FilePath: ".github/workflows/ci.yml", StartLine: 6}}); err != nil {
				t.Fatal(err)
			}
			found = 1
		}
		if err := s.RecordAnalysisMemberResult(runID, held(name).DatabaseIdentity, MemberResult{
			Status: AnalysisSucceeded, Counted: true, Results: found,
			ByQuery: map[string]int{"acme/a": found}, QueryRunID: qr,
		}); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := s.RunFindings(runID, "octo/app", "", 10)
	if err != nil || len(listed.Findings) != 1 {
		t.Fatalf("the fixture run lists %+v (err %v)", listed, err)
	}
	return runID, listed.Findings[0].FindingRowID
}

func pickedRequest(runID int64, evidence ...RequestEvidence) QueryRequest {
	r := aRequest()
	r.QueryID, r.TargetSource, r.Kind, r.SourceRunID = "acme/a", TargetOperator, RequestBoth, runID
	r.Evidence = evidence
	return r
}

// A member counted before members kept their evaluation has results and nothing to pick them
// from, and nothing backfills the link. Saying so is the difference between "run it again" and
// an empty list that reads as "found nothing".
func TestAMemberCountedBeforeItsEvaluationWasKeptSaysSo(t *testing.T) {
	s := newTestStore(t)
	runID, _ := pickedRun(t, s)
	if _, err := s.db.Exec(`UPDATE abom_analysis_member SET query_run_id = NULL
		WHERE run_id = ? AND repository = 'octo/app'`, runID); err != nil {
		t.Fatal(err)
	}

	old, err := s.RunFindings(runID, "octo/app", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if old.Evaluated || !old.Unlinked || len(old.Findings) != 0 {
		t.Errorf("a member counted before its evaluation was kept reads %+v", old)
	}
	for _, repo := range []string{"octo/quiet", "octo/absent"} {
		got, err := s.RunFindings(runID, repo, "", 10)
		if err != nil || got.Unlinked {
			t.Errorf("%s reads %+v (%v), which it was not", repo, got, err)
		}
	}
}

// A request filed from a run keeps what was picked from it, which is what the agent later turns
// into test cases: the finding that should not have fired, the repository where one should have.
func TestARequestFiledFromARunKeepsWhatWasPicked(t *testing.T) {
	s := newTestStore(t)
	runID, fired := pickedRun(t, s)

	want := []RequestEvidence{
		{RunID: runID, Repository: "octo/app", FindingRowID: fired, Kind: RequestFalseAlarm},
		{RunID: runID, Repository: "octo/quiet", Kind: RequestMissed},
	}
	if _, err := s.SaveQueryRequest(pickedRequest(runID, want...)); err != nil {
		t.Fatal(err)
	}
	got, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SourceRunID != runID || !reflect.DeepEqual(got[0].Evidence, want) {
		t.Errorf("filed from run %d with %+v, read back run %d with %+v",
			runID, want, got[0].SourceRunID, got[0].Evidence)
	}
}

// Evidence is what the run showed, or it is an assertion wearing the run's name.
func TestEvidenceIsOnlyWhatTheRunShowed(t *testing.T) {
	s := newTestStore(t)
	runID, fired := pickedRun(t, s)
	other, _ := pickedRun(t, s)

	for name, r := range map[string]QueryRequest{
		"a finding the member's evaluation did not report": pickedRequest(runID,
			RequestEvidence{RunID: runID, Repository: "octo/quiet", FindingRowID: fired, Kind: RequestFalseAlarm}),
		"a miss where nothing ran": pickedRequest(runID,
			RequestEvidence{RunID: runID, Repository: "octo/absent", Kind: RequestMissed}),
		"evidence from another run": pickedRequest(runID,
			RequestEvidence{RunID: other, Repository: "octo/quiet", Kind: RequestMissed}),
		"evidence with no run it came from": pickedRequest(0,
			RequestEvidence{RunID: runID, Repository: "octo/quiet", Kind: RequestMissed}),
		"a false alarm naming no finding": pickedRequest(runID,
			RequestEvidence{RunID: runID, Repository: "octo/app", Kind: RequestFalseAlarm}),
		"a miss naming a finding": pickedRequest(runID,
			RequestEvidence{RunID: runID, Repository: "octo/app", FindingRowID: fired, Kind: RequestMissed}),
	} {
		if _, err := s.SaveQueryRequest(r); err == nil {
			t.Errorf("%s was kept", name)
		}
	}

	missedOnly := pickedRequest(runID,
		RequestEvidence{RunID: runID, Repository: "octo/app", FindingRowID: fired, Kind: RequestFalseAlarm})
	missedOnly.Kind = RequestMissed
	if _, err := s.SaveQueryRequest(missedOnly); err == nil {
		t.Error("a request for a missed detection was kept with a false alarm as its evidence")
	}
	elsewhere := pickedRequest(runID)
	elsewhere.QueryID = "acme/b"
	if _, err := s.SaveQueryRequest(elsewhere); err == nil {
		t.Error("a request about acme/b was filed from a run that never ran it")
	}
	if got, _ := s.QueryRequests(); len(got) != 0 {
		t.Errorf("refused requests left %d rows", len(got))
	}
}

// The evidence is what the run showed when the request was filed. An edit changes what is asked
// for, never what it was asked from.
func TestAnEditLeavesTheEvidenceAsFiled(t *testing.T) {
	s := newTestStore(t)
	runID, fired := pickedRun(t, s)
	filed := []RequestEvidence{
		{RunID: runID, Repository: "octo/app", FindingRowID: fired, Kind: RequestFalseAlarm},
	}
	id, err := s.SaveQueryRequest(pickedRequest(runID, filed...))
	if err != nil {
		t.Fatal(err)
	}

	edit := pickedRequest(0)
	edit.RowID, edit.Description = id, "only the pull_request trigger"
	if _, err := s.SaveQueryRequest(edit); err != nil {
		t.Fatal(err)
	}
	got, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SourceRunID != runID || !reflect.DeepEqual(got[0].Evidence, filed) {
		t.Errorf("after an edit the request reads run %d with %+v", got[0].SourceRunID, got[0].Evidence)
	}
	if got[0].Description != "only the pull_request trigger" {
		t.Errorf("the edit itself was not kept: %q", got[0].Description)
	}
}

func TestARequestWithNoEvidenceListsNoneRatherThanNull(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.SaveQueryRequest(aRequest()); err != nil {
		t.Fatal(err)
	}
	got, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Evidence == nil || got[0].SourceRunID != 0 {
		t.Errorf("a request filed from nothing reads run %d with evidence %#v",
			got[0].SourceRunID, got[0].Evidence)
	}
}

// A request no agent has worked is deleted with what was written in it, leaves every other request
// as it was, and frees its slug to file again.
func TestDeletingARequestNobodyWorkedFreesItsSlug(t *testing.T) {
	s := newTestStore(t)
	doomed := aRequest()
	doomed.Slug = "self-hosted-pr-runner"
	doomed.References = []RequestReference{{Value: "CWE-284"}}
	doomed.Metadata.Tags = []string{"supply-chain"}
	gone, err := s.SaveQueryRequest(doomed)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := s.SaveQueryRequest(aRequest())
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteQueryRequest(gone); err != nil {
		t.Fatal(err)
	}
	left, err := s.QueryRequests()
	if err != nil || len(left) != 1 || left[0].RowID != kept {
		t.Fatalf("after deleting %d, requests are %+v (err %v)", gone, left, err)
	}
	if err := s.DeleteQueryRequest(gone); !errors.Is(err, ErrNoQueryRequest) {
		t.Errorf("deleting it twice: %v, want ErrNoQueryRequest", err)
	}
	if _, err := s.SaveQueryRequest(doomed); err != nil {
		t.Errorf("the deleted request's slug was not freed: %v", err)
	}
}

// An agent's timeline is never rewritten, so a request it worked keeps it and is not deleted.
func TestARequestAnAgentWorkedIsNotDeleted(t *testing.T) {
	s := newTestStore(t)
	request, session := aSessionOn(t, s)
	if err := s.DeleteQueryRequest(request); !errors.Is(err, ErrSessionUnderWay) {
		t.Fatalf("deleting a request being worked: %v, want ErrSessionUnderWay", err)
	}
	if err := s.EndAuthoringSession(session, SessionStopped, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteQueryRequest(request); !errors.Is(err, ErrRequestWorked) {
		t.Fatalf("deleting a request an agent worked: %v, want ErrRequestWorked", err)
	}
	if got, _ := s.QueryRequests(); len(got) != 1 {
		t.Fatalf("a refused delete left %d requests", len(got))
	}
	if sessions, _ := s.AuthoringSessions(request); len(sessions) != 1 {
		t.Errorf("a refused delete left sessions %+v", sessions)
	}
}
