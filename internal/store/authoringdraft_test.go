package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// sessionTableBeforeDrafting is the session table as a store first kept it, before spend and drafts.
const sessionTableBeforeDrafting = `
CREATE TABLE abom_authoring_session (
  row_id     INTEGER PRIMARY KEY,
  request_id INTEGER NOT NULL REFERENCES abom_query_request(row_id),
  state      TEXT NOT NULL,
  started_by TEXT NOT NULL,
  model      TEXT NOT NULL,
  started_at TEXT NOT NULL,
  ended_at   TEXT,
  detail     TEXT NOT NULL DEFAULT ''
);`

// A store that kept sessions before spend and drafts existed opens and gains their columns, rather
// than failing to migrate on the first index that names one.
func TestASessionTableFromBeforeSpendAndDraftsGainsTheirColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(sessionTableBeforeDrafting); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrating a store whose sessions predate spend and drafts: %v", err)
	}
	request, session := aSessionOn(t, s)
	in := int64(5)
	if err := s.AddAuthoringSpend(session, AuthoringSpend{InputTokens: &in}); err != nil {
		t.Fatal(err)
	}
	got, err := s.AuthoringSessions(request)
	if err != nil || len(got) != 1 || got[0].InputTokens == nil || *got[0].InputTokens != 5 {
		t.Errorf("sessions %+v %v", got, err)
	}
}

// A drafting session works from a run's results rather than a request, because nothing is filed
// until a person reads what it drafted; the newest draft it proposed is the one shown.
func TestADraftingSessionProposesFromARunAndFilesNothing(t *testing.T) {
	s := newTestStore(t)
	run := startedRun(t, s)
	if _, err := s.StartDraftSession(run+100, "actions/unpinned-tag", "operator", "claude-opus-5"); err == nil {
		t.Error("a draft was started from a run that does not exist")
	}
	session, err := s.StartDraftSession(run, "actions/unpinned-tag", "operator", "claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartDraftSession(run, "actions/unpinned-tag", "operator", "claude-opus-5"); !errors.Is(err, ErrSessionUnderWay) {
		t.Errorf("a second draft while one runs: %v, want ErrSessionUnderWay", err)
	}
	for _, text := range []string{"first", "second"} {
		if _, err := s.RecordRequestDraft(RequestDraft{SessionID: session, Description: text,
			Compliant: "c", NonCompliant: "n", Rationale: "why"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RecordRequestDraft(RequestDraft{SessionID: session, Description: " ", Compliant: "c",
		NonCompliant: "n"}); err == nil {
		t.Error("a draft with no description was kept")
	}
	if err := s.EndAuthoringSession(session, SessionFinished, "drafted"); err != nil {
		t.Fatal(err)
	}

	drafts, err := s.DraftSessions(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].RowID != session || drafts[0].RunID != run ||
		drafts[0].QueryID != "actions/unpinned-tag" || drafts[0].State != SessionFinished ||
		drafts[0].Draft == nil || drafts[0].Draft.Description != "second" || drafts[0].Draft.Rationale != "why" {
		t.Errorf("drafts %+v", drafts)
	}
	if requests, err := s.QueryRequests(); err != nil || len(requests) != 0 {
		t.Errorf("drafting filed %+v %v", requests, err)
	}
	if again, err := s.StartDraftSession(run, "actions/unpinned-tag", "operator", "claude-opus-5"); err != nil || again == session {
		t.Errorf("a draft after the last ended: %d %v", again, err)
	}
}

// A draft is written from what the run showed, by the rules a request filed from it is held to.
func TestPicksAreHeldToTheRulesARequestFiledFromThemIs(t *testing.T) {
	s := newTestStore(t)
	runID, fired := pickedRun(t, s)
	shown := []RequestEvidence{
		{RunID: runID, Repository: "octo/app", FindingRowID: fired, Kind: RequestFalseAlarm},
		{RunID: runID, Repository: "octo/quiet", Kind: RequestMissed},
	}
	if err := s.CheckPicked(runID, "acme/a", shown); err != nil {
		t.Errorf("picks the run showed were refused: %v", err)
	}
	for name, picks := range map[string][]RequestEvidence{
		"a miss where nothing ran": {{RunID: runID, Repository: "octo/absent", Kind: RequestMissed}},
		"a finding the member did not report": {{RunID: runID, Repository: "octo/quiet",
			FindingRowID: fired, Kind: RequestFalseAlarm}},
		"a false alarm naming no finding": {{RunID: runID, Repository: "octo/app", Kind: RequestFalseAlarm}},
	} {
		if err := s.CheckPicked(runID, "acme/a", picks); err == nil {
			t.Errorf("%s passed", name)
		}
	}
	if err := s.CheckPicked(runID, "acme/b", shown); err == nil {
		t.Error("picks about a query the run never ran passed")
	}
}
