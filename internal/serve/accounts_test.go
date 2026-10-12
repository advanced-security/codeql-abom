package serve

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func post(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := localRequest(http.MethodPost, path, nil)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func put(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := localRequest(http.MethodPut, path, strings.NewReader(body))
	// Changing what discovery will crawl is a control operation, so it carries what one needs.
	req.Host = "127.0.0.1:8080"
	req.Header.Set(controlHeader, "1")
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

type identityView struct {
	Authenticated bool            `json:"authenticated"`
	Login         string          `json:"login"`
	Host          string          `json:"host"`
	Accounts      []store.Account `json:"accounts"`
	Problem       *ghauth.Problem `json:"problem"`
}

func verifies(accounts ...ghauth.Account) func(context.Context) (ghauth.Identity, error) {
	return func(context.Context) (ghauth.Identity, error) {
		return ghauth.Identity{
			Login:      "octocat",
			Host:       "github.com",
			VerifiedAt: time.Now().UTC(),
			Accounts:   accounts,
		}, nil
	}
}

// Until an identity is established the operator owns nothing here. Reporting otherwise would put
// a guess behind every "yours" figure on every page.
func TestAFreshDatabaseOwnsNothing(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	got := decode[identityView](t, get(t, srv, "/api/accounts"))
	if got.Authenticated {
		t.Error("a database that has never been verified reports an authenticated identity")
	}
	if len(got.Accounts) != 0 {
		t.Errorf("owns %+v before anything was verified", got.Accounts)
	}
}

func TestVerifyingRecordsTheAccountsItEstablished(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	srv.Verify = verifies(
		ghauth.Account{Owner: "octocat", Kind: "user", Role: "owner"},
		ghauth.Account{Owner: "octo-org", Kind: "organization", Role: "admin"},
	)

	refreshed := decode[identityView](t, postControl(t, srv, "/api/accounts/refresh"))
	if !refreshed.Authenticated || refreshed.Login != "octocat" {
		t.Fatalf("refresh reported %+v", refreshed)
	}
	if len(refreshed.Accounts) != 2 {
		t.Fatalf("accounts %+v, want the account and its org", refreshed.Accounts)
	}

	// It has to survive the round trip, or every reader re-runs verification.
	stored := decode[identityView](t, get(t, srv, "/api/accounts"))
	if len(stored.Accounts) != 2 || stored.Login != "octocat" {
		t.Errorf("read back %+v", stored)
	}
	for _, a := range stored.Accounts {
		if a.Owner == "octo-org" && a.Role != "admin" {
			t.Errorf("octo-org role %q, want the verified admin", a.Role)
		}
	}
}

// The route that let an account be typed in is gone. While it existed, anything that could reach
// the API could decide which findings counted as the operator's to fix.
func TestAccountsCannotBeDeclaredOverHTTP(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	// With the interface mounted, as it is in production: without it the catch-all route below
	// is never exercised and a PUT answers 405, which says the resource is merely misaddressed.
	srv.UI = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>interface</html>")}}

	for _, tc := range []struct {
		name string
		rec  *httptest.ResponseRecorder
	}{
		{"declaring", put(t, srv, "/api/owned-orgs", `["microsoft"]`)},
		{"reading", get(t, srv, "/api/owned-orgs")},
	} {
		if tc.rec.Code != http.StatusNotFound {
			t.Errorf("%s the withdrawn route: status %d, want 404", tc.name, tc.rec.Code)
		}
		if strings.Contains(tc.rec.Body.String(), "<html>") {
			t.Errorf("%s the withdrawn route was answered with the interface", tc.name)
		}
	}
}

// A broken setup is not the same as belonging to nothing, and the interface has to be able to
// tell them apart: one is fixable, the other is an answer.
func TestAFailedRefreshSaysHowToFixItRatherThanReportingNoAccounts(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	srv.Verify = verifies(ghauth.Account{Owner: "octo-org", Kind: "organization", Role: "member"})
	if rec := postControl(t, srv, "/api/accounts/refresh"); rec.Code != http.StatusOK {
		t.Fatalf("seeding a verification: status %d", rec.Code)
	}

	srv.Verify = func(context.Context) (ghauth.Identity, error) {
		return ghauth.Identity{}, &ghauth.Problem{
			Reason: "gh is not authenticated.", Remedy: "Run: gh auth login",
		}
	}
	got := decode[identityView](t, postControl(t, srv, "/api/accounts/refresh"))

	if got.Problem == nil {
		t.Fatal("a failed refresh reported no problem, so nothing tells the operator what to do")
	}
	if got.Problem.Remedy == "" {
		t.Error("the problem names no remedy")
	}
	// A transient failure must not silently redraw every split in the interface.
	if len(got.Accounts) != 1 {
		t.Errorf("a failed refresh discarded the previous verification: %+v", got.Accounts)
	}
}

// Candidates are drawn from what was analyzed. They must never arrive marked as yours: an
// account holding analyzed repositories is not thereby one the operator belongs to.
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
			t.Errorf("%s is offered as already owned", c.Owner)
		}
	}
}

// Both halves of every split are served, so a reader can see what they can act on without losing
// sight of what the corpus holds.
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
		t.Errorf("nothing verified, yet %d references report as yours", before.Yours.Yours)
	}
	if before.Yours.Elsewhere != before.Pinnable {
		t.Errorf("undivided corpus should all be elsewhere: %d of %d",
			before.Yours.Elsewhere, before.Pinnable)
	}

	// The split counts what the operator has declared theirs; the halves must still sum to the whole.
	if rec := put(t, srv, "/api/owned-scope",
		`[{"owner":"octo-org","kind":"organization","enabled":true}]`); rec.Code != http.StatusOK {
		t.Fatalf("declaring: status %d: %s", rec.Code, rec.Body.String())
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

// Verification runs a subprocess, so a refresh must never be reachable by a plain page load.
func TestRefreshingRequiresMoreThanAGet(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	srv.Verify = func(context.Context) (ghauth.Identity, error) {
		return ghauth.Identity{}, errors.New("verification must not run for a GET")
	}

	if rec := get(t, srv, "/api/accounts/refresh"); rec.Code == http.StatusOK {
		t.Errorf("status %d: a GET triggered verification", rec.Code)
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
