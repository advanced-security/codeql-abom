package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/authz"
)

func queued(t *testing.T, s *Store, decision string) (request, session int64) {
	t.Helper()
	request, err := s.SaveQueryRequest(aRequest())
	if err != nil {
		t.Fatal(err)
	}
	session, err = s.QueueAuthoringSession(request, "operator", "claude-opus-5", decision)
	if err != nil {
		t.Fatal(err)
	}
	return request, session
}

func sessionOf(t *testing.T, s *Store, request, session int64) AuthoringSession {
	t.Helper()
	sessions, err := s.AuthoringSessions(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range sessions {
		if a.RowID == session {
			return a
		}
	}
	t.Fatalf("session %d is not among request %d's", session, request)
	return AuthoringSession{}
}

// Sessions past the limit wait in the order they were asked for, each saying how many wait ahead of
// it, and none has started.
func TestSessionsWaitInTheOrderTheyWereAskedFor(t *testing.T) {
	s := newTestStore(t)
	var requests, sessions []int64
	for range 3 {
		request, session := queued(t, s, "")
		requests, sessions = append(requests, request), append(sessions, session)
	}
	waiting, err := s.QueuedAuthoringSessions()
	if err != nil || len(waiting) != 3 {
		t.Fatalf("waiting %+v %v", waiting, err)
	}
	for i, a := range waiting {
		if a.RowID != sessions[i] {
			t.Errorf("waiting %d is session %d, want %d", i, a.RowID, sessions[i])
		}
	}
	for i := range sessions {
		a := sessionOf(t, s, requests[i], sessions[i])
		if a.State != SessionQueued || a.Ahead != i || a.QueuedAt == "" || a.StartedAt != "" {
			t.Errorf("session %d reads %+v; want queued since a time, not started, with %d ahead", sessions[i], a, i)
		}
	}
}

// A request has one session waiting or working at a time, whichever way it was asked for.
func TestARequestHasOneSessionWaitingOrWorking(t *testing.T) {
	s := newTestStore(t)
	request, _ := queued(t, s, "")
	if _, err := s.QueueAuthoringSession(request, "operator", "claude-opus-5", ""); !errors.Is(err, ErrSessionUnderWay) {
		t.Errorf("a second session waiting on one request: %v", err)
	}
	if _, err := s.StartAuthoringSession(request, "operator", "claude-opus-5"); !errors.Is(err, ErrSessionUnderWay) {
		t.Errorf("a session started on a request one waits on: %v", err)
	}
	if _, err := s.QueueAuthoringSession(999, "operator", "claude-opus-5", ""); !errors.Is(err, ErrNoQueryRequest) {
		t.Errorf("a session waiting on no request: %v", err)
	}
}

// A waiting session begins once, as running from then; one stopped while waiting never started.
func TestAWaitingSessionBeginsOnceOrIsStoppedUnstarted(t *testing.T) {
	s := newTestStore(t)
	request, session := queued(t, s, "")
	if err := s.BeginQueuedSession(session); err != nil {
		t.Fatal(err)
	}
	if a := sessionOf(t, s, request, session); a.State != SessionRunning || a.StartedAt == "" || a.Ahead != 0 {
		t.Errorf("the begun session reads %+v", a)
	}
	if err := s.BeginQueuedSession(session); err == nil {
		t.Error("a session began twice")
	}

	request, session = queued(t, s, "")
	if err := s.EndAuthoringSession(session, SessionStopped, "stopped before it started"); err != nil {
		t.Fatal(err)
	}
	if a := sessionOf(t, s, request, session); a.State != SessionStopped || a.StartedAt != "" || a.EndedAt == "" {
		t.Errorf("the session stopped while waiting reads %+v", a)
	}
	if err := s.BeginQueuedSession(session); err == nil {
		t.Error("a stopped session began")
	}
}

// A restart ends the sessions it was running, and the ones waiting keep waiting.
func TestARestartLeavesWaitingSessionsWaiting(t *testing.T) {
	s := newTestStore(t)
	running, begun := queued(t, s, "")
	if err := s.BeginQueuedSession(begun); err != nil {
		t.Fatal(err)
	}
	waiting, session := queued(t, s, "")
	if _, err := s.InterruptAuthoringSessions(); err != nil {
		t.Fatal(err)
	}
	if a := sessionOf(t, s, running, begun); a.State != SessionInterrupted {
		t.Errorf("the running session reads %s after a restart", a.State)
	}
	if a := sessionOf(t, s, waiting, session); a.State != SessionQueued {
		t.Errorf("the waiting session reads %s after a restart", a.State)
	}
}

// A waiting session keeps the decision that asked for it, which verifies as the log holds it; a
// digest the log does not hold, or one changed, verifies as nothing.
func TestAWaitingSessionKeepsTheDecisionThatAskedForIt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	point, err := s.DecisionPoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d, err := point.Decide(ctx, authz.Request{Operation: authz.OpAuthoring, Principal: authz.Local(""),
		Subject: authz.RouteSubject("POST /api/query-requests/{id}/sessions"), Allow: true})
	if err != nil {
		t.Fatal(err)
	}
	_, session := queued(t, s, d.Digest())
	digest, err := s.SessionDecision(session)
	if err != nil || digest != d.Digest() {
		t.Fatalf("the session keeps decision %q, %v; want %q", digest, err, d.Digest())
	}
	got, err := s.VerifiedDecision(digest)
	if err != nil || got.Operation() != authz.OpAuthoring || !got.Allowed() {
		t.Errorf("the decision verifies as %+v, %v", got, err)
	}
	for _, digest := range []string{"", "0000000000000000000000000000000000000000000000000000000000000000"} {
		if _, err := s.VerifiedDecision(digest); err == nil {
			t.Errorf("decision %q verified", digest)
		}
	}
}

// A session a worker runs records the attempt and the grant it runs under, so a coordinator that
// restarts finds the reports it had not yet kept, and only for sessions still held as running.
func TestASessionRecordsTheAttemptAWorkerRunsItUnder(t *testing.T) {
	s := newTestStore(t)
	_, session := queued(t, s, "")
	if err := s.SetSessionAttempt(session, "a1", "g1"); err == nil {
		t.Error("an attempt was recorded for a session that has not begun")
	}
	if err := s.BeginQueuedSession(session); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionAttempt(session, "a1", "g1"); err != nil {
		t.Fatal(err)
	}
	_, local := queued(t, s, "")
	if err := s.BeginQueuedSession(local); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestAuthoringReport(session, AuthoringReport{Seq: 1, Digest: "d1",
		Entries: []AuthoringEntry{eventEntry(session, "view")}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.RunningRemoteSessions()
	if err != nil || len(got) != 1 || got[0] != (RemoteSession{Session: session, Attempt: "a1", Grant: "g1",
		JournalSeq: 1, JournalHead: "d1"}) {
		t.Errorf("the sessions workers run read %+v %v", got, err)
	}
	if _, err := s.InterruptAuthoringSessions(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.RunningRemoteSessions(); err != nil || len(got) != 0 {
		t.Errorf("after a restart ended them, the sessions workers run read %+v %v", got, err)
	}
}

// picks are what a draft is asked for from: a false alarm where the run found it, and a repository
// where it found nothing it should have.
var picks = []DraftPick{
	{Repository: "octo/app", Kind: RequestFalseAlarm, FilePath: ".github/workflows/ci.yml", StartLine: 4,
		Message: "runs on a self-hosted runner"},
	{Repository: "octo/lib", Kind: RequestMissed},
}

// A draft past the limit waits its turn in the same queue as sessions on requests, with the picks it
// was asked for from and the decision that asked, and a run has one draft waiting or working at a time.
func TestADraftWaitsItsTurnWithWhatItDraftsFrom(t *testing.T) {
	s := newTestStore(t)
	_, first := queued(t, s, "")
	run := startedRun(t, s)
	if _, err := s.QueueDraftSession(run, "actions/unpinned-tag", "operator", "claude-opus-5", "d1", nil); err == nil {
		t.Error("a draft was asked for from no picks")
	}
	if _, err := s.QueueDraftSession(run+100, "actions/unpinned-tag", "operator", "claude-opus-5", "d1", picks); err == nil {
		t.Error("a draft was asked for from a run that does not exist")
	}
	draft, err := s.QueueDraftSession(run, "actions/unpinned-tag", "operator", "claude-opus-5", "d1", picks)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueueDraftSession(run, "actions/unpinned-tag", "operator", "claude-opus-5", "d2", picks); !errors.Is(err, ErrSessionUnderWay) {
		t.Errorf("a second draft waiting on one run answered %v", err)
	}
	if _, err := s.StartDraftSession(run, "actions/unpinned-tag", "operator", "claude-opus-5"); !errors.Is(err, ErrSessionUnderWay) {
		t.Errorf("a draft started beside one waiting on its run answered %v", err)
	}
	waiting, err := s.QueuedAuthoringSessions()
	if err != nil || len(waiting) != 2 || waiting[0].RowID != first || waiting[1].RowID != draft ||
		waiting[1].RunID != run || waiting[1].QueryID != "actions/unpinned-tag" || waiting[1].RequestID != 0 {
		t.Fatalf("waiting %+v %v; want the request's session, then the draft on its run", waiting, err)
	}
	drafts, err := s.DraftSessions(run)
	if err != nil || len(drafts) != 1 || drafts[0].State != SessionQueued || drafts[0].Ahead != 1 ||
		drafts[0].QueuedAt == "" || drafts[0].StartedAt != "" {
		t.Errorf("the run's drafts read %+v %v; want it waiting with one ahead", drafts, err)
	}
	if decision, err := s.SessionDecision(draft); err != nil || decision != "d1" {
		t.Errorf("the draft's decision reads %q %v", decision, err)
	}
	if err := s.BeginQueuedSession(draft); err != nil {
		t.Fatal(err)
	}
	snap, err := s.AuthoringSnapshot(draft)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snap.Picks, picks) || snap.Run == nil || snap.Run.RowID != run ||
		snap.Session.QueryID != "actions/unpinned-tag" {
		t.Errorf("the draft's snapshot reads picks %+v, run %+v, query %q", snap.Picks, snap.Run, snap.Session.QueryID)
	}
}

// The queue is read in its own order from an index, however many sessions have ever been kept.
func TestSQLiteSessionsWaitingAreReadFromAnIndex(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateIndexes(); err != nil {
		t.Fatal(err)
	}
	plan := explain(t, s, `SELECT row_id FROM abom_authoring_session WHERE state = ? ORDER BY queued_at, row_id`,
		SessionQueued)
	if !strings.Contains(plan, "idx_authoring_session_queue") || strings.Contains(plan, "TEMP B-TREE") {
		t.Errorf("the sessions waiting are read by %s", plan)
	}
}
