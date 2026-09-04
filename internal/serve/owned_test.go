package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func put(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// The declaration is the only ownership notion the operator sets, so it has to survive the
// round trip exactly: an account silently dropped would quietly widen what reads as "not yours".
func TestOwnedOrgsSurviveTheRoundTrip(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	if got := decode[[]string](t, get(t, srv, "/api/owned-orgs")); len(got) != 0 {
		t.Errorf("a fresh database reports %v as declared", got)
	}

	rec := put(t, srv, "/api/owned-orgs", `["octo-org","Octo-Org"]`)
	stored := decode[[]string](t, rec)
	if len(stored) != 1 || stored[0] != "octo-org" {
		t.Errorf("stored %v, want one account: GitHub account names are case-insensitive", stored)
	}

	if got := decode[[]string](t, get(t, srv, "/api/owned-orgs")); len(got) != 1 {
		t.Errorf("declared one account, read back %v", got)
	}
}

// A malformed body must be refused rather than silently clearing the declaration, which would
// turn a client bug into an unexplained change in every figure on every page.
func TestMalformedDeclarationIsRejected(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	if rec := put(t, srv, "/api/owned-orgs", `["octo-org"]`); rec.Code != http.StatusOK {
		t.Fatalf("seeding the declaration: status %d", rec.Code)
	}

	rec := put(t, srv, "/api/owned-orgs", `{"owner":"octo-org"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d for a malformed body, want 400", rec.Code)
	}

	if got := decode[[]string](t, get(t, srv, "/api/owned-orgs")); len(got) != 1 {
		t.Errorf("a rejected request changed the declaration to %v", got)
	}
}

// Candidates are a suggestion drawn from what was analysed. They must never arrive pre-selected:
// an account holding analysed repositories is not thereby one the operator controls.
func TestOwnerCandidatesArriveUnselected(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	type candidate struct {
		Owner        string `json:"owner"`
		Repositories int    `json:"repositories"`
		Owned        bool   `json:"owned"`
	}
	got := decode[[]candidate](t, get(t, srv, "/api/owner-candidates"))
	if len(got) == 0 {
		t.Fatal("no candidates offered for a corpus with a repository in scope")
	}
	for _, c := range got {
		if c.Owned {
			t.Errorf("%s is offered as already declared", c.Owner)
		}
	}
}

// Both halves of every split are served, so a reader can see what they can act on without
// losing sight of what the corpus holds.
func TestPinningTotalsCarryBothHalvesOfTheSplit(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	type split struct {
		Yours     int `json:"yours"`
		Elsewhere int `json:"elsewhere"`
	}
	type totals struct {
		Pinnable     int   `json:"pinnable"`
		Flagged      int   `json:"flagged"`
		Yours        split `json:"yours"`
		FlaggedSplit split `json:"flaggedSplit"`
	}

	before := decode[totals](t, get(t, srv, "/api/pinning/totals"))
	if before.Yours.Yours != 0 {
		t.Errorf("nothing declared, yet %d references report as yours", before.Yours.Yours)
	}
	if before.Yours.Elsewhere != before.Pinnable {
		t.Errorf("undivided corpus should all be elsewhere: %d of %d",
			before.Yours.Elsewhere, before.Pinnable)
	}

	if rec := put(t, srv, "/api/owned-orgs", `["octo-org"]`); rec.Code != http.StatusOK {
		t.Fatalf("declaring: status %d", rec.Code)
	}

	after := decode[totals](t, get(t, srv, "/api/pinning/totals"))
	if after.Pinnable != before.Pinnable || after.Flagged != before.Flagged {
		t.Errorf("declaring changed the totals: %d/%d became %d/%d",
			before.Pinnable, before.Flagged, after.Pinnable, after.Flagged)
	}
	if after.Yours.Yours+after.Yours.Elsewhere != after.Pinnable {
		t.Errorf("split %d + %d != %d", after.Yours.Yours, after.Yours.Elsewhere, after.Pinnable)
	}
	if after.Yours.Yours == 0 {
		t.Error("declared the account that owns the only repository, yet nothing is yours")
	}
}

// Each level names its parent with the same key the parent reported, so a client can walk the
// tree without reconstructing keys of its own.
func TestPinningLevelsDrillByTheKeyTheParentReported(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	type row struct {
		ScopeKey    string `json:"scopeKey"`
		Pinnable    int    `json:"pinnable"`
		HasChildren bool   `json:"hasChildren"`
	}

	owners := decode[[]row](t, get(t, srv, "/api/pinning?scope=owner"))
	if len(owners) == 0 {
		t.Fatal("no owners with pinnable references")
	}

	repos := decode[[]row](t, get(t, srv,
		"/api/pinning?scope=repo&parent="+owners[0].ScopeKey))
	if len(repos) == 0 {
		t.Fatalf("owner %s holds %d references but drills to nothing",
			owners[0].ScopeKey, owners[0].Pinnable)
	}

	workflows := decode[[]row](t, get(t, srv,
		"/api/pinning?scope=workflow&parent="+repos[0].ScopeKey))
	if len(workflows) == 0 {
		t.Fatalf("repository %s drills to no workflows", repos[0].ScopeKey)
	}

	refs := decode[[]row](t, get(t, srv,
		"/api/pinning?scope=reference&parent="+workflows[0].ScopeKey))
	if len(refs) == 0 {
		t.Fatalf("workflow %s drills to no references", workflows[0].ScopeKey)
	}
	for _, r := range refs {
		if r.HasChildren {
			t.Errorf("reference %s reports children; it is the leaf", r.ScopeKey)
		}
	}
}
