package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func aSessionOn(t *testing.T, s *Store) (request, session int64) {
	t.Helper()
	request, err := s.SaveQueryRequest(aRequest())
	if err != nil {
		t.Fatal(err)
	}
	session, err = s.StartAuthoringSession(request, "operator", "claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	return request, session
}

func TestASessionIsStartedOnlyOnARequestThatExists(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.StartAuthoringSession(999, "operator", "claude-opus-5"); !errors.Is(err, ErrNoQueryRequest) {
		t.Errorf("a session on no request: %v, want ErrNoQueryRequest", err)
	}
	request, session := aSessionOn(t, s)
	got, err := s.AuthoringSessions(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RowID != session || got[0].State != SessionRunning ||
		got[0].StartedBy != "operator" || got[0].Model != "claude-opus-5" || got[0].StartedAt == "" ||
		got[0].EndedAt != "" {
		t.Errorf("sessions %+v", got)
	}
}

// The request list says which requests an agent is working on without reading each one's sessions.
func TestTheRequestsUnderWayAreReadInOneGo(t *testing.T) {
	s := newTestStore(t)
	working, session := aSessionOn(t, s)
	ended, done := aSessionOn(t, s)
	if err := s.EndAuthoringSession(done, SessionFinished, "done"); err != nil {
		t.Fatal(err)
	}

	got, err := s.RunningAuthoringSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[working] != session {
		t.Errorf("running %v, want only request %d with session %d", got, working, session)
	}
	if _, ok := got[ended]; ok {
		t.Errorf("request %d reads running after its session ended", ended)
	}
}

// What a session spends arrives one model call at a time, so a stopped session still says what it
// cost; a figure the CLI never reported stays absent rather than reading as zero.
func TestASessionAddsUpWhatItSpentAsTheCLIReportsIt(t *testing.T) {
	s := newTestStore(t)
	request, session := aSessionOn(t, s)
	spent := func() AuthoringSession {
		t.Helper()
		got, err := s.AuthoringSessions(request)
		if err != nil || len(got) != 1 {
			t.Fatalf("sessions %v %v", got, err)
		}
		return got[0]
	}
	if a := spent(); a.ModelCalls != nil || a.InputTokens != nil || a.OutputTokens != nil || a.NanoAIU != nil {
		t.Fatalf("a session the CLI reported nothing for says it spent %+v", a)
	}

	in, out, nano := int64(1200), int64(80), int64(250_000_000)
	if err := s.AddAuthoringSpend(session, AuthoringSpend{InputTokens: &in, NanoAIU: &nano}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAuthoringSpend(session, AuthoringSpend{InputTokens: &in, OutputTokens: &out}); err != nil {
		t.Fatal(err)
	}
	if err := s.EndAuthoringSession(session, SessionStopped, "stopped by an operator"); err != nil {
		t.Fatal(err)
	}
	a := spent()
	switch {
	case a.ModelCalls == nil || *a.ModelCalls != 2:
		t.Errorf("model calls %v, want 2", a.ModelCalls)
	case a.InputTokens == nil || *a.InputTokens != 2400:
		t.Errorf("input tokens %v, want 2400", a.InputTokens)
	case a.OutputTokens == nil || *a.OutputTokens != 80:
		t.Errorf("output tokens %v, want 80", a.OutputTokens)
	case a.NanoAIU == nil || *a.NanoAIU != 250_000_000:
		t.Errorf("nano-AIU %v, want 250000000", a.NanoAIU)
	}
	if err := s.AddAuthoringSpend(session+100, AuthoringSpend{InputTokens: &in}); err == nil {
		t.Error("spend was added to a session that does not exist")
	}
}

// Two agents writing one query's files would each see the other's work as its own.
func TestOneSessionAtATimeWorksARequest(t *testing.T) {
	s := newTestStore(t)
	request, session := aSessionOn(t, s)
	if _, err := s.StartAuthoringSession(request, "operator", "claude-opus-5"); !errors.Is(err, ErrSessionUnderWay) {
		t.Fatalf("second session while one runs: %v, want ErrSessionUnderWay", err)
	}
	if err := s.EndAuthoringSession(session, SessionFinished, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartAuthoringSession(request, "operator", "claude-opus-5"); err != nil {
		t.Errorf("a new session after the last ended: %v", err)
	}
}

func TestEveryEventIsKeptInTheOrderItHappened(t *testing.T) {
	s := newTestStore(t)
	_, session := aSessionOn(t, s)
	took := int64(8000)
	events := []AuthoringEvent{
		{SessionID: session, At: "2026-09-23T12:00:00Z", Hook: "pre-tool-use", Tool: "bash",
			Subject: "rm -rf ~", Decision: "refused", Reason: "no shell"},
		{SessionID: session, At: "2026-09-23T12:00:01Z", Hook: "permission",
			Tool: "ql-mcp-codeql_test_run", Subject: "test/A", Decision: "allowed", Step: "tests"},
		{SessionID: session, At: "2026-09-23T12:00:09Z", Hook: "post-tool-use",
			Tool: "ql-mcp-codeql_test_run", Subject: "test/A", Decision: "succeeded", Step: "tests",
			TookMs: &took},
	}
	var ids []int64
	for _, e := range events {
		id, err := s.RecordAuthoringEvent(e)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	got, err := s.AuthoringEvents(session, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	for i, e := range got {
		if e.RowID != ids[i] || e.Tool != events[i].Tool || e.Decision != events[i].Decision ||
			e.Subject != events[i].Subject || e.Reason != events[i].Reason {
			t.Errorf("event %d came back as %+v", i, e)
		}
	}
	// Not measured is not measured as zero.
	if got[0].TookMs != nil || got[2].TookMs == nil || *got[2].TookMs != took {
		t.Errorf("durations %v and %v", got[0].TookMs, got[2].TookMs)
	}
	body, _ := json.Marshal(got[0])
	if !strings.Contains(string(body), `"tookMs":null`) {
		t.Errorf("an unmeasured event marshals as %s", body)
	}

	// A reader polling a live session asks for what came after the last event it holds.
	after, err := s.AuthoringEvents(session, ids[0], 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || after[0].RowID != ids[1] {
		t.Errorf("after %d: %+v", ids[0], after)
	}
	if _, err := s.RecordAuthoringEvent(AuthoringEvent{SessionID: 999, At: "x", Hook: "h", Tool: "t",
		Decision: "d"}); err == nil {
		t.Error("an event kept for a session that does not exist")
	}
}

func TestASessionEndsOnceAndSaysHow(t *testing.T) {
	s := newTestStore(t)
	request, session := aSessionOn(t, s)
	if err := s.EndAuthoringSession(session, "exploded", ""); err == nil {
		t.Error("a session ended in a state nobody defined")
	}
	if err := s.EndAuthoringSession(session, SessionStopped, "stopped by operator"); err != nil {
		t.Fatal(err)
	}
	if err := s.EndAuthoringSession(session, SessionFinished, "done"); !errors.Is(err, ErrSessionNotUnderWay) {
		t.Errorf("ending an ended session: %v, want ErrSessionNotUnderWay", err)
	}
	got, err := s.AuthoringSessions(request)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].State != SessionStopped || got[0].Detail != "stopped by operator" || got[0].EndedAt == "" {
		t.Errorf("ended as %+v", got[0])
	}
}

// A server that died left the row saying running; nothing is running it, and nothing failed in it
// either, so it reads as neither.
func TestASessionTheServerLostIsInterruptedNotFailed(t *testing.T) {
	s := newTestStore(t)
	request, lost := aSessionOn(t, s)
	other, err := s.SaveQueryRequest(aRequest())
	if err != nil {
		t.Fatal(err)
	}
	ended, err := s.StartAuthoringSession(other, "operator", "claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndAuthoringSession(ended, SessionFinished, "done"); err != nil {
		t.Fatal(err)
	}
	n, err := s.InterruptAuthoringSessions()
	if err != nil || n != 1 {
		t.Fatalf("interrupted %d (%v), want 1", n, err)
	}
	got, _ := s.AuthoringSessions(request)
	if got[0].RowID != lost || got[0].State != SessionInterrupted || got[0].EndedAt == "" {
		t.Errorf("lost session %+v", got[0])
	}
	kept, _ := s.AuthoringSessions(other)
	if kept[0].State != SessionFinished {
		t.Errorf("an ended session was touched: %+v", kept[0])
	}
}

// Refusals are what an operator looks for first: an agent that kept hitting the bounds is the
// session to read.
func TestTheSessionListCountsEventsAndRefusals(t *testing.T) {
	s := newTestStore(t)
	request, session := aSessionOn(t, s)
	for _, d := range []string{"refused", "allowed", "refused", "succeeded"} {
		if _, err := s.RecordAuthoringEvent(AuthoringEvent{SessionID: session, At: "2026-09-23T12:00:00Z",
			Hook: "pre-tool-use", Tool: "view", Decision: d}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.AuthoringSessions(request)
	if got[0].Events != 4 || got[0].Refused != 2 {
		t.Errorf("counted %d events and %d refusals, want 4 and 2", got[0].Events, got[0].Refused)
	}
	empty, err := s.AuthoringSessions(12345)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("no sessions: %v %v, want an empty list", empty, err)
	}
}

// The timeline is the record of what an agent tried; one that can be edited records nothing.
func TestTheAuthoringTimelineIsNeverRewritten(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	rewrites := regexp.MustCompile(`(UPDATE|DELETE FROM|REPLACE INTO|INSERT OR REPLACE INTO)\s+(abom_authoring_(event|ast|review)|abom_request_draft)\b`)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skip := skipNonSource(root, path, d); skip != nil {
			return skip
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if rewrites.Match(body) {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s rewrites the authoring timeline", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTheAgentDecidesATargetOnlyWhereNobodyChoseOne(t *testing.T) {
	s := newTestStore(t)
	open, err := s.SaveQueryRequest(aRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DecideTarget(open, "acme/checkout-tag-pin",
		"nothing in the catalog reports a tag-pinned checkout"); err != nil {
		t.Fatal(err)
	}
	r := requestNamed(t, s, open)
	if r.QueryID != "acme/checkout-tag-pin" || r.TargetSource != TargetAgent ||
		r.TargetDetail != "nothing in the catalog reports a tag-pinned checkout" {
		t.Errorf("decided %+v", r)
	}
	if err := s.DecideTarget(open, "acme/other", "second thoughts"); err == nil {
		t.Error("a decided target decided again")
	}
	if err := s.DecideTarget(open, "", "nothing"); err == nil {
		t.Error("an empty target decided")
	}

	// An operator's edit is their act, so the agent's reasoning no longer describes the target.
	r.Description = "edited"
	r.TargetSource = TargetOperator
	if _, err := s.SaveQueryRequest(r); err != nil {
		t.Fatal(err)
	}
	if edited := requestNamed(t, s, open); edited.TargetDetail != "" {
		t.Errorf("an operator's edit kept the agent's reasoning: %q", edited.TargetDetail)
	}

	chosen := aRequest()
	chosen.QueryID, chosen.TargetSource = "acme/self-hosted-runner", TargetOperator
	id, err := s.SaveQueryRequest(chosen)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DecideTarget(id, "acme/other", "better"); err == nil {
		t.Error("the agent overrode the operator's choice")
	}
	if err := s.DecideTarget(999, "acme/x", "x"); !errors.Is(err, ErrNoQueryRequest) {
		t.Errorf("deciding for no request: %v, want ErrNoQueryRequest", err)
	}
}

func requestNamed(t *testing.T, s *Store, id int64) QueryRequest {
	t.Helper()
	all, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if r.RowID == id {
			return r
		}
	}
	t.Fatalf("no request %d", id)
	return QueryRequest{}
}
