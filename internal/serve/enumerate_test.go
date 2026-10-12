package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/enumerate"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Declaring an account says which repositories are the operator's. Nothing acted on it: on the
// reference corpus the operator declared two accounts holding 397 repositories and the corpus held
// one of them, because a repository only ever entered by being somebody else's dependency. The
// declaration has to be enumerated for the corpus to be about the operator's own work.
func TestEnumeratingADeclaredAccountBringsItsRepositoriesIn(t *testing.T) {
	srv, st := newServer(t)
	declare(t, st, "advanced-security")
	srv.Enumerate = fakeEnumerator(map[string][]string{
		"advanced-security": {"advanced-security/one", "advanced-security/two"},
	})

	var report EnumerationReport
	postJSON(t, srv, "/api/owned-scope/enumerate", &report)

	if report.Added != 2 {
		t.Errorf("Added = %d, want 2", report.Added)
	}
	if got := repositories(t, st); !slices.Equal(got, []string{
		"advanced-security/one", "advanced-security/two"}) {
		t.Errorf("corpus holds %v, want the declared account's repositories", got)
	}
}

// A repository first seen as somebody else's dependency is recorded `upstream`, so it is a leaf of
// their report and a root of nobody's. Enumerating the account that owns it is new information.
func TestEnumerationPromotesARepositoryThatArrivedAsADependency(t *testing.T) {
	srv, st := newServer(t)
	declare(t, st, "advanced-security")
	if _, err := st.InsertUpstreamRepository("advanced-security/dismiss-alerts"); err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}
	srv.Enumerate = fakeEnumerator(map[string][]string{
		"advanced-security": {"advanced-security/dismiss-alerts"},
	})

	var report EnumerationReport
	postJSON(t, srv, "/api/owned-scope/enumerate", &report)

	if report.Promoted != 1 {
		t.Errorf("Promoted = %d, want the dependency counted as promoted", report.Promoted)
	}
	if report.Added != 0 {
		t.Errorf("Added = %d, want it counted once, as a promotion", report.Added)
	}
}

// An account that is declared but not enabled is a recorded intention, not an instruction. Acting
// on it would crawl repositories the operator deliberately left switched off.
func TestADisabledDeclarationIsNotEnumerated(t *testing.T) {
	srv, st := newServer(t)
	if err := st.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "advanced-security", Kind: "organization", Enabled: false},
	}, store.DeclaredByInterface); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	srv.Enumerate = fakeEnumerator(map[string][]string{
		"advanced-security": {"advanced-security/one"},
	})

	var report EnumerationReport
	postJSON(t, srv, "/api/owned-scope/enumerate", &report)

	if len(repositories(t, st)) != 0 {
		t.Error("a disabled declaration was enumerated")
	}
	if len(report.Accounts) != 0 {
		t.Errorf("reported %v, want no account enumerated", report.Accounts)
	}
}

// One unreachable account must not discard the accounts that answered. Enumeration is several
// independent calls, and reporting nothing because the last one failed loses the rest.
func TestOneFailingAccountDoesNotDiscardTheOthers(t *testing.T) {
	srv, st := newServer(t)
	declare(t, st, "advanced-security", "data-douser")
	srv.Enumerate = func(_ context.Context, account string, _ catalog.Marks) (catalog.Listing, error) {
		if account == "data-douser" {
			return catalog.Listing{}, errors.New("rate limited")
		}
		return catalog.Listing{
			Repositories: []catalog.Repository{{FullName: "advanced-security/one"}},
			Route:        catalog.RouteGraphQL, Full: true,
		}, nil
	}

	var report EnumerationReport
	postJSON(t, srv, "/api/owned-scope/enumerate", &report)

	if report.Added != 1 {
		t.Errorf("Added = %d, want the account that answered to be kept", report.Added)
	}
	if len(report.Failed) != 1 || !strings.Contains(report.Failed[0].Error, "rate limited") {
		t.Errorf("Failed = %+v, want the failure named rather than swallowed", report.Failed)
	}
}

// An account that holds nothing is a fact worth reporting: it is how an operator sees that they
// declared a name that is not theirs, or is empty.
func TestAnAccountThatHoldsNothingIsReported(t *testing.T) {
	srv, st := newServer(t)
	declare(t, st, "advanced-security")
	srv.Enumerate = fakeEnumerator(map[string][]string{"advanced-security": {}})

	var report EnumerationReport
	postJSON(t, srv, "/api/owned-scope/enumerate", &report)

	if len(report.Accounts) != 1 || report.Accounts[0].Found != 0 {
		t.Errorf("Accounts = %+v, want the empty account reported", report.Accounts)
	}
}

// Enumeration clones nothing and runs no queries, but it does write the corpus and spend the
// operator's rate limit, so it is held to the same control gate as the rest.
func TestEnumerationIsControlGuarded(t *testing.T) {
	srv, _ := newServer(t)
	req := localRequest(http.MethodPost, "/api/owned-scope/enumerate", nil)
	req.RemoteAddr = "203.0.113.7:9999"
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// A build without an enumerator must say so rather than reporting an empty corpus as an accurate
// one.
func TestWithoutAnEnumeratorTheServerSaysSo(t *testing.T) {
	srv, st := newServer(t)
	declare(t, st, "advanced-security")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, controlRequest("/api/owned-scope/enumerate"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

// Recording a repository is not analyzing it. Enumeration that only writes rows leaves the
// operator with 198 repositories in the corpus, none of them analyzed and none of them queued —
// which reads exactly like having none at all.
func TestEnumerationQueuesWhatItFound(t *testing.T) {
	srv, st := newServer(t)
	declare(t, st, "advanced-security")
	srv.Enumerate = fakeEnumerator(map[string][]string{
		"advanced-security": {"advanced-security/one", "advanced-security/two"},
	})
	queued := 0
	srv.QueueOwned = func() (int, error) { queued = 2; return 2, nil }

	var report EnumerationReport
	postJSON(t, srv, "/api/owned-scope/enumerate", &report)

	if queued != 2 || report.Queued != 2 {
		t.Errorf("queued %d and reported %d, want both 2", queued, report.Queued)
	}
}

// Queueing runs after the repositories are recorded. Queueing first would enqueue the previous
// enumeration and miss everything this one found.
func TestQueueingFollowsRecording(t *testing.T) {
	srv, st := newServer(t)
	declare(t, st, "advanced-security")
	srv.Enumerate = fakeEnumerator(map[string][]string{
		"advanced-security": {"advanced-security/one"},
	})
	var sawWhenQueued []string
	srv.QueueOwned = func() (int, error) {
		sawWhenQueued, _ = st.OwnedRepositories()
		return len(sawWhenQueued), nil
	}

	var report EnumerationReport
	postJSON(t, srv, "/api/owned-scope/enumerate", &report)

	if len(sawWhenQueued) != 1 {
		t.Errorf("queueing saw %v, want the repository just recorded", sawWhenQueued)
	}
}

// A queue that could not be written must not be reported as one that was. The repositories are
// recorded either way, so the failure is about analysis and not about the declaration.
func TestAFailedQueueIsReportedWithoutLosingTheEnumeration(t *testing.T) {
	srv, st := newServer(t)
	declare(t, st, "advanced-security")
	srv.Enumerate = fakeEnumerator(map[string][]string{
		"advanced-security": {"advanced-security/one"},
	})
	srv.QueueOwned = func() (int, error) { return 0, errors.New("queue is locked") }

	var report EnumerationReport
	postJSON(t, srv, "/api/owned-scope/enumerate", &report)

	if report.Added != 1 {
		t.Errorf("Added = %d, want the enumeration kept", report.Added)
	}
	if !strings.Contains(report.QueueError, "queue is locked") {
		t.Errorf("QueueError = %q, want the failure named", report.QueueError)
	}
}

// The button and the loop read an account through one path, protected branches included, so what a
// person pressing it records is what the loop records on its period.
func TestEnumeratingFromTheInterfaceAsksWhatEachRepositoryProtects(t *testing.T) {
	srv, st := newServer(t)
	declare(t, st, "advanced-security")
	srv.Enumerate = fakeEnumerator(map[string][]string{
		"advanced-security": {"advanced-security/one"},
	})
	var asked []string
	srv.EnumerateBranches = func(_ context.Context, repository string) ([]string, error) {
		asked = append(asked, repository)
		return []string{"main"}, nil
	}

	var report EnumerationReport
	postJSON(t, srv, "/api/owned-scope/enumerate", &report)

	if !slices.Equal(asked, []string{"advanced-security/one"}) {
		t.Errorf("asked about %v, want the repository the listing named", asked)
	}
	if _, known, err := st.ProtectedBranches("advanced-security/one"); err != nil || !known {
		t.Errorf("ProtectedBranches known=%v err=%v, want the answer recorded", known, err)
	}
	if report.Accounts[0].BranchesRead != 1 {
		t.Errorf("BranchesRead = %d, want 1", report.Accounts[0].BranchesRead)
	}
}

func fakeEnumerator(byAccount map[string][]string) enumerate.Read {
	return func(_ context.Context, account string, _ catalog.Marks) (catalog.Listing, error) {
		repos, ok := byAccount[account]
		if !ok {
			return catalog.Listing{}, fmt.Errorf("account %q was not expected", account)
		}
		out := make([]catalog.Repository, 0, len(repos))
		for _, r := range repos {
			out = append(out, catalog.Repository{FullName: r})
		}
		return catalog.Listing{Repositories: out, Route: catalog.RouteGraphQL, Full: true}, nil
	}
}

func declare(t *testing.T, st *store.Store, owners ...string) {
	t.Helper()
	var accounts []store.DeclaredAccount
	for _, o := range owners {
		accounts = append(accounts, store.DeclaredAccount{
			Owner: o, Kind: "organization", Enabled: true})
	}
	if err := st.SetOwnedScope(accounts, store.DeclaredByInterface); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
}

func postJSON(t *testing.T, srv *Server, path string, out any) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, controlRequest(path))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s: status %d: %s", path, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
}

func repositories(t *testing.T, st *store.Store) []string {
	t.Helper()
	names, err := st.OwnedRepositories()
	if err != nil {
		t.Fatalf("OwnedRepositories: %v", err)
	}
	sort.Strings(names)
	return names
}
