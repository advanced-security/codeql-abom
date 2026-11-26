package serve

import (
	"net/http"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Settings needs to say whether the declaration on record drives anything: one saved here is signed,
// and one that names no decision is shown as needing to be saved again.
func TestTheDeclarationsDecisionIsServed(t *testing.T) {
	srv, s := newServer(t)
	if got := decode[store.ScopeVerdict](t, get(t, srv, "/api/owned-scope/decision")); got.State != store.ScopeNone {
		t.Errorf("nothing declared reads %+v; want none", got)
	}

	if rec := control(t, srv, http.MethodPut, "/api/owned-scope",
		`[{"owner":"octo-org","kind":"organization","enabled":true}]`); rec.Code != http.StatusOK {
		t.Fatalf("declaring: status %d: %s", rec.Code, rec.Body.String())
	}
	signed := decode[store.ScopeVerdict](t, get(t, srv, "/api/owned-scope/decision"))
	if signed.State != store.ScopeSigned || signed.Digest == "" || signed.DecidedAt == "" {
		t.Errorf("a declaration saved here reads %+v; want it signed", signed)
	}

	if _, err := s.DB().Exec(`UPDATE abom_owned_scope SET decision_digest = NULL`); err != nil {
		t.Fatal(err)
	}
	if got := decode[store.ScopeVerdict](t, get(t, srv, "/api/owned-scope/decision")); got.State != store.ScopeUnsigned || got.Reason == "" {
		t.Errorf("an undecided declaration reads %+v; want unsigned with the reason", got)
	}
}
