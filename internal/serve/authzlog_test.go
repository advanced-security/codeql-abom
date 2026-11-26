package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
)

// Anybody who can read ABOM can read what it decided, in order, a page at a time from where they
// stopped, with the chain value that lets them check it.
func TestTheAuthorizationLogIsReadInOrderFromWhereAReaderStopped(t *testing.T) {
	s, st := newServer(t)
	signer, err := authz.OpenLocal(filepath.Join(t.TempDir(), "authz"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p := &authz.Point{Signer: signer, Ledger: st}
	for _, subject := range []string{"octo", "acme", "has-ghas"} {
		if _, err := p.Decide(context.Background(), authz.Request{Operation: authz.OpDeclareScope, Subject: subject, Allow: true}); err != nil {
			t.Fatal(err)
		}
	}

	var page struct {
		Entries []authz.Entry `json:"entries"`
	}
	rec := get(t, s, "/api/audit/authz?after=1&limit=1")
	if err := json.Unmarshal(rec.Body.Bytes(), &page); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("answered %d: %s", rec.Code, rec.Body.String())
	}
	if len(page.Entries) != 1 || page.Entries[0].Seq != 2 || page.Entries[0].PredicateType != authz.PredicateDecision ||
		len(page.Entries[0].Envelope) == 0 {
		t.Errorf("entries = %+v", page.Entries)
	}
	rec = get(t, s, "/api/audit/authz")
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Entries) != 3 {
		t.Errorf("the whole log: %d entries, %v", len(page.Entries), err)
	}
	if rec := get(t, s, "/api/audit/authz?after=x"); rec.Code != http.StatusBadRequest {
		t.Errorf("a cursor that is not a number answered %d", rec.Code)
	}
}
