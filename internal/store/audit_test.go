package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAuditPagesKeepTheirCutoffAndCountEveryMatch(t *testing.T) {
	s := newTestStore(t)
	request, session := aSessionOn(t, s)
	for i := 0; i < 603; i++ {
		_, err := s.RecordAuthoringEvent(AuthoringEvent{SessionID: session, At: "2026-10-01T00:00:00Z",
			Hook: "pre-tool-use", Tool: "view", Decision: "allowed", Subject: "100% literal"})
		if err != nil {
			t.Fatal(err)
		}
	}
	f := AuditFilter{Request: request, Text: "100%", Limit: 100}
	page, err := s.AuditEvents(f)
	if err != nil || page.Total != 603 || len(page.Items) != 100 || !page.HasMore {
		t.Fatalf("first page: %+v, %v", page, err)
	}
	_, err = s.RecordAuthoringEvent(AuthoringEvent{SessionID: session, At: "2026-10-01T00:00:00Z",
		Hook: "pre-tool-use", Tool: "view", Decision: "allowed", Subject: "100% literal"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for {
		for _, e := range page.Items {
			if seen[e.RowID] {
				t.Fatalf("duplicate %d", e.RowID)
			}
			seen[e.RowID] = true
		}
		if !page.HasMore {
			break
		}
		f.Cursor = page.NextCursor
		page, err = s.AuditEvents(f)
		if err != nil || page.Total != 603 {
			t.Fatalf("subsequent page: %+v, %v", page, err)
		}
	}
	if len(seen) != 603 {
		t.Fatalf("saw %d events", len(seen))
	}
	f.Tool = "edit"
	if _, err := s.AuditEvents(f); err == nil {
		t.Fatal("cursor accepted a changed filter")
	}
}

// A reader following a session oldest first sits on its last page, where the next events land.
// The cursor kept its cutoff, so asking it for a later one reloaded the same page for ever and the
// new events never appeared. Oldest first, a later cutoff only extends the walk past where the page
// stopped; newest first it would put rows ahead of pages already read, and an earlier one would
// drop rows already shown, so both are still refused.
func TestAnOldestFirstCursorExtendsToALaterCutoff(t *testing.T) {
	s := newTestStore(t)
	_, session := aSessionOn(t, s)
	record := func() int64 {
		t.Helper()
		id, err := s.RecordAuthoringEvent(AuthoringEvent{SessionID: session, At: "2026-10-01T00:00:00Z",
			Hook: "pre-tool-use", Tool: "view", Decision: "allowed"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	for range 3 {
		record()
	}
	newest, err := s.AuditEvents(AuditFilter{Session: session, Order: "desc", Limit: 2})
	if err != nil || !newest.HasMore {
		t.Fatalf("newest first: %+v, %v", newest, err)
	}
	f := AuditFilter{Session: session, Order: "asc", Limit: 2}
	first, err := s.AuditEvents(f)
	if err != nil || !first.HasMore {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	f.Cursor = first.NextCursor
	last, err := s.AuditEvents(f)
	if err != nil || last.HasMore || len(last.Items) != 1 {
		t.Fatalf("last page: %+v, %v", last, err)
	}
	fourth, fifth := record(), record()

	f.Through, f.FixedCutoff = fifth, true
	grown, err := s.AuditEvents(f)
	if err != nil {
		t.Fatalf("a later cutoff on the last oldest-first page was refused: %v", err)
	}
	if grown.Through != fifth || grown.Total != 5 || len(grown.Items) != 2 ||
		grown.Items[0].RowID != last.Items[0].RowID || grown.Items[1].RowID != fourth || !grown.HasMore {
		t.Fatalf("the page did not grow in place to the later cutoff: %+v", grown)
	}

	for name, refused := range map[string]AuditFilter{
		"an earlier cutoff":           {Session: session, Order: "asc", Limit: 2, Cursor: first.NextCursor, Through: last.Through - 1, FixedCutoff: true},
		"newest first, a later one":   {Session: session, Order: "desc", Limit: 2, Cursor: newest.NextCursor, Through: fifth, FixedCutoff: true},
		"another filter, a later one": {Session: session, Order: "asc", Limit: 2, Cursor: first.NextCursor, Through: fifth, FixedCutoff: true, Tool: "edit"},
	} {
		if _, err := s.AuditEvents(refused); err == nil {
			t.Errorf("%s was accepted with an existing cursor", name)
		}
	}
}

// A filter the store refuses is marked as one, so a caller can say it was the reader's to correct
// without reading the wording; a store failure is not marked, whatever it says.
func TestARefusedAuditFilterIsMarkedAsOne(t *testing.T) {
	s := newTestStore(t)
	for name, f := range map[string]AuditFilter{
		"limit":  {Limit: 501},
		"order":  {Order: "sideways"},
		"cursor": {Cursor: "not-a-cursor"},
	} {
		if _, err := s.AuditEvents(f); !errors.Is(err, ErrBadAuditFilter) {
			t.Errorf("%s: %v is not marked as a refused filter", name, err)
		}
	}
	if _, err := s.AuditEvents(AuditFilter{Request: 999}); errors.Is(err, ErrBadAuditFilter) {
		t.Errorf("an unrecorded request is marked as a refused filter: %v", err)
	}
}

// A request, run or session that is not recorded is named in the refusal: "no recorded session"
// answered a filter on a request that did not exist, and named nothing a reader could correct.
func TestAnUnrecordedOwnerIsNamedInTheRefusal(t *testing.T) {
	s := newTestStore(t)
	for name, f := range map[string]AuditFilter{
		"request 999": {Request: 999},
		"run 998":     {Run: 998},
		"session 997": {Session: 997},
	} {
		if _, err := s.AuditEvents(f); !errors.Is(err, ErrNoAuditSession) || err.Error() != "no "+name+" is recorded" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.AuditSession(996); !errors.Is(err, ErrNoAuditSession) || err.Error() != "no session 996 is recorded" {
		t.Errorf("session 996: %v", err)
	}
}

func TestHistoricAuditDetailsAreProjectedWithoutRewritingThem(t *testing.T) {
	s := newTestStore(t)
	_, session := aSessionOn(t, s)
	raw := `{"token":"historic-canary","file_text":"private source"}`
	_, err := s.db.Exec(`INSERT INTO abom_authoring_event(session_id,at,hook,tool,decision,detail)
		VALUES (?,'2026-10-01T00:00:00Z','message','agent','noted',?)`, session, raw)
	if err != nil {
		t.Fatal(err)
	}

	page, err := s.AuditEvents(AuditFilter{Session: session})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("%+v, %v", page, err)
	}
	body, _ := json.Marshal(page)
	if strings.Contains(string(body), "historic-canary") || strings.Contains(string(body), "private source") {
		t.Fatalf("unsafe projection: %s", body)
	}
	var kept string
	if err := s.db.QueryRow(`SELECT detail FROM abom_authoring_event WHERE session_id=?`, session).Scan(&kept); err != nil || kept != raw {
		t.Fatalf("historic row changed: %s, %v", kept, err)
	}
	meta, err := s.AuditSession(session)
	if err != nil || len(meta.Policies) != 0 || !strings.Contains(meta.Coverage, "not recorded") {
		t.Fatalf("invented historic policy: %+v %v", meta, err)
	}
}
