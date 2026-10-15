package store

import (
	"testing"
	"time"
)

func TestARequestCannotClaimAnUnrelatedAgentDraft(t *testing.T) {
	s := newTestStore(t)
	_, err := s.SaveQueryRequest(QueryRequest{Description: "d", Compliant: "c", NonCompliant: "n", RequestedBy: "operator", SourceDraftID: 999})
	if err == nil {
		t.Fatal("a fabricated draft relation was accepted")
	}
}

func TestAuditPagesBoundTheHundredThousandEventCase(t *testing.T) {
	s := newTestStore(t)
	_, session := aSessionOn(t, s)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO abom_authoring_event(session_id,at,hook,tool,decision)
		VALUES (?,'2026-10-01T00:00:00Z','pre-tool-use','view','allowed')`)
	if err != nil {
		t.Fatal(err)
	}
	for range 100000 {
		if _, err := stmt.Exec(session); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, order := range []string{"asc", "desc"} {
		f := AuditFilter{Session: session, Order: order, Limit: 500}
		start := time.Now()
		first, err := s.AuditEvents(f)
		if err != nil || first.Total != 100000 || len(first.Items) != 500 {
			t.Fatalf("first: total=%d items=%d %v", first.Total, len(first.Items), err)
		}
		f.Cursor = first.NextCursor
		next, err := s.AuditEvents(f)
		if err != nil || next.Total != 100000 || len(next.Items) != 500 {
			t.Fatalf("next: total=%d items=%d %v", next.Total, len(next.Items), err)
		}
		if first.Items[499].RowID == next.Items[0].RowID {
			t.Fatal("page duplicated boundary")
		}
		seen := map[int64]bool{}
		page := first
		for {
			for _, event := range page.Items {
				if seen[event.RowID] {
					t.Fatalf("duplicate event %d", event.RowID)
				}
				seen[event.RowID] = true
			}
			if !page.HasMore {
				break
			}
			f.Cursor = page.NextCursor
			page, err = s.AuditEvents(f)
			if err != nil {
				t.Fatal(err)
			}
		}
		if len(seen) != 100000 {
			t.Fatalf("only %d events traversed", len(seen))
		}
		t.Logf("%s: traversed 100000 events in %s with pages <=500", order, time.Since(start))
	}
	start := time.Now()
	text, err := s.AuditEvents(AuditFilter{Text: "view", Limit: 100})
	if err != nil || text.Total != 100000 || len(text.Items) != 100 {
		t.Fatalf("text scan: %d %d %v", text.Total, len(text.Items), err)
	}
	t.Logf("global sanitized text search: 100000 rows, first 100 returned, %s", time.Since(start))
}
