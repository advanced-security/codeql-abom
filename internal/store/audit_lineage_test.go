package store

import "testing"

func TestRunAuditUsesRecordedVersionsAndDeduplicatesOverlappingRelations(t *testing.T) {
	s := newTestStore(t)
	run := startedRun(t, s)
	request, err := s.SaveQueryRequest(QueryRequest{Description: "d", Compliant: "c", NonCompliant: "n", RequestedBy: "operator", QueryID: "actions/unpinned-tag", TargetSource: TargetOperator})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.StartAuthoringSession(request, "operator", "test")
	if err != nil {
		t.Fatal(err)
	}
	event := AuthoringEvent{SessionID: session, At: "2026-10-01T00:00:00Z", Hook: "pre-tool-use", Tool: "view", Decision: "allowed", QueryID: "actions/unpinned-tag"}
	if _, err := s.RecordAuthoringEvent(event); err != nil {
		t.Fatal(err)
	}
	page, err := s.AuditEvents(AuditFilter{Run: run})
	if err != nil || page.Total != 0 {
		t.Fatalf("same query inferred provenance: %+v %v", page, err)
	}
	event.QueryVersion = "u1"
	if _, err := s.RecordAuthoringEvent(event); err != nil {
		t.Fatal(err)
	}
	page, err = s.AuditEvents(AuditFilter{Run: run})
	if err != nil || page.Total != 2 {
		t.Fatalf("explicit version lost: %+v %v", page, err)
	}
	draftSession, err := s.StartDraftSession(run, "actions/unpinned-tag", "operator", "test")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.RecordRequestDraft(RequestDraft{SessionID: draftSession, Description: "d", Compliant: "c", NonCompliant: "n"})
	if err != nil {
		t.Fatal(err)
	}
	filed, err := s.SaveQueryRequest(QueryRequest{Description: "d", Compliant: "c", NonCompliant: "n", RequestedBy: "operator",
		QueryID: "actions/unpinned-tag", SourceRunID: run, SourceDraftID: draft, TargetSource: TargetOperator})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := s.StartAuthoringSession(filed, "operator", "test")
	if err != nil {
		t.Fatal(err)
	}
	event.SessionID = worked
	if _, err := s.RecordAuthoringEvent(event); err != nil {
		t.Fatal(err)
	}
	page, err = s.AuditEvents(AuditFilter{Run: run})
	if err != nil || page.Total != 3 {
		t.Fatalf("overlap duplicated events: %+v %v", page, err)
	}
	meta, err := s.AuditSession(draftSession)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range meta.Relations {
		if r.Kind == "request filed using draft" && r.RequestID == filed && r.DraftID == draft {
			found = true
		}
	}
	if !found {
		t.Fatalf("draft relation missing: %+v", meta.Relations)
	}
	scoped, err := s.AuditSessions(request, run)
	if err != nil || len(scoped) != 1 || scoped[0].RowID != session {
		t.Fatalf("combined scope broadened: %d %v", len(scoped), err)
	}
}

func TestRestartRecordsRecoveryWithoutInventingTheMissingEnd(t *testing.T) {
	s := newTestStore(t)
	_, session := aSessionOn(t, s)
	if _, err := s.InterruptAuthoringSessions(); err != nil {
		t.Fatal(err)
	}
	page, err := s.AuditEvents(AuditFilter{Session: session})
	if err != nil || page.Total != 1 || page.Items[0].Hook != "abom-session-recovery" {
		t.Fatalf("restart observation missing: %+v %v", page, err)
	}
	if _, err := s.InterruptAuthoringSessions(); err != nil {
		t.Fatal(err)
	}
	page, err = s.AuditEvents(AuditFilter{Session: session})
	if err != nil || page.Total != 1 {
		t.Fatal("restart duplicated recovery")
	}
}
