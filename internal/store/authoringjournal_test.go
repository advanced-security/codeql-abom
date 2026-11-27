package store

import (
	"strings"
	"testing"
)

func eventEntry(session int64, tool string) AuthoringEntry {
	return AuthoringEntry{Kind: EntryEvent, Event: &AuthoringEvent{SessionID: session,
		At: "2026-11-26T12:00:00Z", Hook: "pre-tool-use", Tool: tool, Decision: "allowed"}}
}

func countEvents(t *testing.T, s *Store, session int64) int {
	t.Helper()
	page, err := s.AuditEvents(AuditFilter{Session: session})
	if err != nil {
		t.Fatal(err)
	}
	return page.Total
}

// A worker's report is replayed once, in the order the reports were made, and whole: one replayed
// twice changes nothing, one out of order or not following the one before is refused, and one that
// cannot be kept in full keeps none of it.
func TestAReportIsReplayedOnceInOrderAndWhole(t *testing.T) {
	s := newTestStore(t)
	request, err := s.SaveQueryRequest(QueryRequest{Description: "report a tag pin", Compliant: "a",
		NonCompliant: "b", Kind: RequestMissed, RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.StartAuthoringSession(request, "operator", "claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	nano := int64(7)
	first := AuthoringReport{Seq: 1, Digest: "d1", Entries: []AuthoringEntry{
		eventEntry(session, "view"),
		{Kind: EntryTarget, Target: &TargetDecision{QueryID: "acme/pin", Rationale: "none fits"}},
		{Kind: EntryTree, Tree: &AuthoringAST{QueryID: "acme/pin", PrintedAt: "2026-11-26T12:01:00Z",
			Query: "PrintAST", Database: "test/Pin/Pin.testproj", Body: "#1 Job a\n", CLIVersion: "2.27.6"}},
		{Kind: EntryTest, Test: &QueryTest{QueryID: "acme/pin", QueryVersion: "v1", CLIVersion: "2.27.6",
			Total: 1, Failed: 1, FailureStage: "RESULT", RanAt: "2026-11-26T12:02:00Z"}},
		{Kind: EntrySpend, Spend: &AuthoringSpend{NanoAIU: &nano}},
		{Kind: EntryModel, Model: "auto"},
	}}
	if _, err := s.IngestAuthoringReport(session, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestAuthoringReport(session, first); err != nil {
		t.Fatalf("a report replayed twice was refused: %v", err)
	}
	if n := countEvents(t, s, session); n != 1 {
		t.Errorf("the report replayed twice left %d events, want 1", n)
	}
	sessions, err := s.AuthoringSessions(request)
	if err != nil || len(sessions) != 1 || sessions[0].NanoAIU == nil || *sessions[0].NanoAIU != 7 ||
		sessions[0].Model != "auto" {
		t.Errorf("sessions %+v %v; want the spend kept once and the model the report named", sessions, err)
	}
	tests, err := s.QueryTestsOf("acme/pin")
	if err != nil || len(tests) != 1 || tests[0].RanAt != "2026-11-26T12:02:00Z" {
		t.Errorf("tests %+v %v; want the run kept at the time the worker ran it", tests, err)
	}
	if _, err := s.AuthoringASTByDigest(session, TreeDigest("#1 Job a\n")); err != nil {
		t.Errorf("the tree the report kept: %v", err)
	}

	for name, r := range map[string]AuthoringReport{
		"out of order":                 {Seq: 3, Digest: "d3", Prev: "d1", Entries: []AuthoringEntry{eventEntry(session, "grep")}},
		"not following the one before": {Seq: 2, Digest: "d2", Prev: "elsewhere", Entries: []AuthoringEntry{eventEntry(session, "grep")}},
		"testing another query": {Seq: 2, Digest: "d2", Prev: "d1", Entries: []AuthoringEntry{eventEntry(session, "grep"),
			{Kind: EntryTest, Test: &QueryTest{QueryID: "acme/other", QueryVersion: "v1", CLIVersion: "2.27.6"}}}},
		"drafting on a request": {Seq: 2, Digest: "d2", Prev: "d1", Entries: []AuthoringEntry{eventEntry(session, "grep"),
			{Kind: EntryDraft, Draft: &RequestDraft{Description: "d", Compliant: "a", NonCompliant: "b"}}}},
		"printing for another query": {Seq: 2, Digest: "d2", Prev: "d1", Entries: []AuthoringEntry{eventEntry(session, "grep"),
			{Kind: EntryTree, Tree: &AuthoringAST{QueryID: "acme/other", PrintedAt: "2026-11-26T12:03:00Z",
				Query: "PrintAST", Database: "test/Other/Other.testproj", Body: "#1 Job b\n", CLIVersion: "2.27.6"}}}},
		"rewriting a kept report": {Seq: 1, Digest: "elsewhere", Entries: []AuthoringEntry{eventEntry(session, "grep")}},
	} {
		if _, err := s.IngestAuthoringReport(session, r); err == nil {
			t.Errorf("a report %s was kept", name)
		}
	}
	if n := countEvents(t, s, session); n != 1 {
		t.Errorf("refused reports left %d events, want only the first report's", n)
	}
	if _, err := s.IngestAuthoringReport(session, AuthoringReport{Seq: 2, Digest: "d2", Prev: "d1",
		Entries: []AuthoringEntry{eventEntry(session, "grep")}}); err != nil {
		t.Errorf("the report that follows was refused: %v", err)
	}
}

// What a report says happened is kept against the session that reported it, whatever session an
// entry names: a worker writes nowhere else, and links nothing it reports to another session's record.
func TestAReportWritesOnlyIntoItsOwnSession(t *testing.T) {
	s := newTestStore(t)
	_, session := aSessionOn(t, s)
	_, other := aSessionOn(t, s)
	policy, err := s.RecordAuthoringEvent(AuthoringEvent{SessionID: other, At: "2026-11-26T11:59:00Z",
		Hook: "policy", Tool: "session", Decision: "noted"})
	if err != nil {
		t.Fatal(err)
	}
	forged := eventEntry(other, "view")
	forged.Event.PolicyID = policy
	if _, err := s.IngestAuthoringReport(session, AuthoringReport{Seq: 1, Digest: "d1",
		Entries: []AuthoringEntry{forged}}); err != nil {
		t.Fatal(err)
	}
	if mine, theirs := countEvents(t, s, session), countEvents(t, s, other); mine != 1 || theirs != 1 {
		t.Errorf("the event went to session %d (%d) rather than %d (%d)", other, theirs-1, session, mine)
	}
	page, err := s.AuditEvents(AuditFilter{Session: session})
	if err != nil || len(page.Items) != 1 || page.Items[0].PolicyID != 0 {
		t.Errorf("the reported event reads %+v %v; want it under no policy, since its session recorded none", page.Items, err)
	}
}

// A session tests only the query its request is about, so a report testing one before the session
// decided which describes a run that session could not have made.
func TestAReportKeepsNoTestBeforeATarget(t *testing.T) {
	s := newTestStore(t)
	_, session := aSessionOn(t, s)
	if _, err := s.IngestAuthoringReport(session, AuthoringReport{Seq: 1, Digest: "d1", Entries: []AuthoringEntry{
		{Kind: EntryTest, Test: &QueryTest{QueryID: "acme/pin", QueryVersion: "v1", CLIVersion: "2.27.6"}}}}); err == nil {
		t.Error("a test kept before the session decided what its request is about")
	}
}

// Once a session has ended, nothing more is kept for it: a report arriving after the coordinator
// stopped waiting describes work nobody is landing.
func TestAReportOnAnEndedSessionIsRefused(t *testing.T) {
	s := newTestStore(t)
	_, session := aSessionOn(t, s)
	if _, err := s.InterruptAuthoringSessions(); err != nil {
		t.Fatal(err)
	}
	_, err := s.IngestAuthoringReport(session, AuthoringReport{Seq: 1, Digest: "d1",
		Entries: []AuthoringEntry{eventEntry(session, "view")}})
	if err == nil || !strings.Contains(err.Error(), SessionInterrupted) {
		t.Errorf("a report on an ended session answered %v", err)
	}
}

// The end a worker reports is handed back rather than kept: whether what it did lands, and so how the
// session ends, is the coordinator's to decide.
func TestTheEndAWorkerReportsIsHandedBack(t *testing.T) {
	s := newTestStore(t)
	request, session := aSessionOn(t, s)
	end, err := s.IngestAuthoringReport(session, AuthoringReport{Seq: 1, Digest: "d1", Entries: []AuthoringEntry{
		eventEntry(session, "view"), {Kind: EntryEnd, End: &SessionEnd{State: SessionFinished, Detail: "done"}}}})
	if err != nil || end == nil || end.State != SessionFinished || end.Detail != "done" {
		t.Fatalf("the end handed back is %+v, %v", end, err)
	}
	sessions, err := s.AuthoringSessions(request)
	if err != nil || len(sessions) != 1 || sessions[0].State != SessionRunning {
		t.Errorf("sessions %+v %v; want it still running until the coordinator ends it", sessions, err)
	}
}

// A worker reads what the session would read from the store as it stood when the session started:
// its request, the catalog, and every query's tests and newest tree.
func TestASnapshotHoldsWhatASessionReads(t *testing.T) {
	s := newTestStore(t)
	request, session := aSessionOn(t, s)
	if err := s.RecordQueryTest(QueryTest{QueryID: "acme/pin", QueryVersion: "v1", CLIVersion: "2.27.6",
		Total: 1, Failed: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAuthoringAST(AuthoringAST{SessionID: session, QueryID: "acme/pin",
		PrintedAt: "2026-11-26T12:00:00Z", Query: "PrintAST", Database: "test/Pin/Pin.testproj",
		Body: "#1 Job a\n", CLIVersion: "2.27.6"}); err != nil {
		t.Fatal(err)
	}

	// The session decides what it is about as it works, so it reads tests of any query on record.
	if err := s.RecordQueryTest(QueryTest{QueryID: "acme/other", QueryVersion: "v2", CLIVersion: "2.27.6",
		Total: 1, Passed: true}); err != nil {
		t.Fatal(err)
	}

	snap, err := s.AuthoringSnapshot(session)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case snap.Request == nil || snap.Request.RowID != request:
		t.Errorf("the snapshot's request is %+v", snap.Request)
	case snap.Session.RowID != session || snap.Session.RequestID != request:
		t.Errorf("the snapshot's session is %+v", snap.Session)
	case len(snap.Sessions) != 1 || snap.Sessions[0].RowID != session:
		t.Errorf("the snapshot's session row is %+v", snap.Sessions)
	case len(snap.Tests) != 2 || len(snap.Tests["acme/pin"]) != 1 || len(snap.Tests["acme/other"]) != 1:
		t.Errorf("the snapshot's tests are %+v; want every query's on record", snap.Tests)
	case snap.Trees["acme/pin"].Digest != TreeDigest("#1 Job a\n"):
		t.Errorf("the snapshot's newest trees are %+v", snap.Trees)
	case snap.Catalog == nil:
		t.Error("the snapshot's catalog is null rather than empty")
	}
}

// What a session spent only grows: a report of spend below nothing would hide what it cost.
func TestAReportSpendingLessThanNothingIsRefused(t *testing.T) {
	s := newTestStore(t)
	less := int64(-1000)
	for name, spend := range map[string]AuthoringSpend{
		"input": {InputTokens: &less}, "output": {OutputTokens: &less}, "credits": {NanoAIU: &less},
	} {
		_, session := aSessionOn(t, s)
		if _, err := s.IngestAuthoringReport(session, AuthoringReport{Seq: 1, Digest: "d1",
			Entries: []AuthoringEntry{{Kind: EntrySpend, Spend: &spend}}}); err == nil {
			t.Errorf("a report spending less than nothing in %s was kept", name)
		}
	}
}

// A session drafting from a run decides no target, prints no tree and runs no test, and one on a
// request drafts nothing: a report saying otherwise describes work its session could not have done.
func TestAReportKeepsOnlyWhatItsKindOfSessionMakes(t *testing.T) {
	s := newTestStore(t)
	for name, e := range map[string]AuthoringEntry{
		"a target": {Kind: EntryTarget, Target: &TargetDecision{QueryID: "actions/unpinned-tag", Rationale: "it fits"}},
		"a tree": {Kind: EntryTree, Tree: &AuthoringAST{PrintedAt: "2026-11-26T12:00:00Z", Query: "PrintAST",
			Database: "test/Q/Q.testproj", Body: "#1", CLIVersion: "2.27.1", Digest: TreeDigest("#1")}},
		"a test": {Kind: EntryTest, Test: &QueryTest{QueryID: "actions/unpinned-tag", QueryVersion: "v1",
			CLIVersion: "2.27.6"}},
	} {
		drafting, err := s.StartDraftSession(startedRun(t, s), "actions/unpinned-tag", "operator", "claude-opus-5")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.IngestAuthoringReport(drafting, AuthoringReport{Seq: 1, Digest: "d1",
			Entries: []AuthoringEntry{e}}); err == nil {
			t.Errorf("a session drafting from a run kept %s", name)
		}
	}
	_, session := aSessionOn(t, s)
	if _, err := s.IngestAuthoringReport(session, AuthoringReport{Seq: 1, Digest: "d1", Entries: []AuthoringEntry{
		{Kind: EntryDraft, Draft: &RequestDraft{Description: "report a tag", Compliant: "uses: a/b@0123",
			NonCompliant: "uses: a/b@v1", Rationale: "x"}}}}); err == nil {
		t.Error("a session on a request kept a draft")
	}
}
