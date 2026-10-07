package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/store"
)

type listedAccount struct {
	Owner        string              `json:"owner"`
	Repositories []ghauth.Repository `json:"repositories"`
	Problem      *ghauth.Problem     `json:"problem"`
}

type listCall struct {
	owner string
	own   bool
}

func listingServer(t *testing.T, answer func(owner string) ([]ghauth.Repository, error)) (*Server, *store.Store, *[]listCall) {
	t.Helper()
	srv, s := newServer(t)
	var calls []listCall
	srv.ListRepositories = func(_ context.Context, owner string, own bool) ([]ghauth.Repository, error) {
		calls = append(calls, listCall{owner, own})
		return answer(owner)
	}
	return srv, s, &calls
}

func listRepositories(t *testing.T, srv *Server, owner string) (*httptest.ResponseRecorder, listedAccount) {
	t.Helper()
	rec := postControl(t, srv, "/api/accounts/"+owner+"/repositories")
	var out listedAccount
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding %s: %v", rec.Body, err)
		}
	}
	return rec, out
}

// Listing is what the include picker offers, and every name in it must be GitHub's answer rather
// than whatever the store happens to hold.
func TestAnAccountsRepositoriesAreListedFromGitHubsAnswer(t *testing.T) {
	srv, s, _ := listingServer(t, func(string) ([]ghauth.Repository, error) {
		return []ghauth.Repository{{Name: "tooling", Private: true}}, nil
	})
	seedGraph(t, s)

	rec, out := listRepositories(t, srv, "has-ghas")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if out.Owner != "has-ghas" || len(out.Repositories) != 1 || out.Repositories[0].Name != "tooling" {
		t.Errorf("got %+v, want has-ghas listing only tooling", out)
	}
	if out.Problem != nil {
		t.Errorf("problem %+v on a listing that succeeded", out.Problem)
	}
}

// `users/{name}/repos` hides the operator's private repositories, so whether the account is the
// operator's own decides which route gh takes. It is decided from the recorded verification, not
// from anything the request says.
func TestTheOperatorsOwnAccountIsListedAsTheirs(t *testing.T) {
	srv, s, calls := listingServer(t, func(string) ([]ghauth.Repository, error) { return nil, nil })
	if err := s.ReplaceAccounts(store.Verification{Login: "Octocat", Host: "github.com",
		VerifiedAt: time.Now(), Accounts: []store.Account{
			{Owner: "Octocat", Kind: store.AccountUser, Role: store.RoleOwner},
			{Owner: "octo-org", Kind: store.AccountOrganization, Role: store.RoleAdmin},
		}}); err != nil {
		t.Fatalf("recording verification: %v", err)
	}

	listRepositories(t, srv, "octocat")
	listRepositories(t, srv, "octo-org")

	want := []listCall{{"octocat", true}, {"octo-org", false}}
	if len(*calls) != len(want) {
		t.Fatalf("calls %+v, want %+v", *calls, want)
	}
	for i := range want {
		if (*calls)[i] != want[i] {
			t.Errorf("call %d is %+v, want %+v", i, (*calls)[i], want[i])
		}
	}
}

// An empty account and an unanswered request are opposite answers. The list is always present,
// so a client reading it never mistakes null for "holds nothing".
func TestAnAccountHoldingNothingIsListedAsEmptyNotNull(t *testing.T) {
	srv, _, _ := listingServer(t, func(string) ([]ghauth.Repository, error) { return nil, nil })

	rec, _ := listRepositories(t, srv, "empty-org")

	if !strings.Contains(rec.Body.String(), `"repositories":[]`) {
		t.Errorf("body %s, want an empty list rather than null", rec.Body)
	}
}

// A gh failure is something the reader can fix, so it arrives as the remedy beside an empty list
// rather than as a fault that leaves the page unable to say what would help.
func TestAListingGhRefusedCarriesTheRemedy(t *testing.T) {
	srv, _, _ := listingServer(t, func(owner string) ([]ghauth.Repository, error) {
		return nil, &ghauth.Problem{Reason: owner + " requires single sign-on.", Remedy: "authorize it"}
	})

	rec, out := listRepositories(t, srv, "codeql")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: a gh refusal was reported as a fault", rec.Code)
	}
	if out.Problem == nil || out.Problem.Remedy != "authorize it" {
		t.Errorf("problem %+v, want the remedy carried", out.Problem)
	}
}

// Anything else that went wrong is ABOM's, and says so.
func TestAListingThatFailsOtherwiseIsAFault(t *testing.T) {
	srv, _, _ := listingServer(t, func(string) ([]ghauth.Repository, error) {
		return nil, errors.New("decoding the answer")
	})

	rec, _ := listRepositories(t, srv, "has-ghas")

	if rec.Code < 500 {
		t.Errorf("status %d, want a server fault", rec.Code)
	}
}

// It spends the operator's token and names repositories that token can see, so it is asked for
// the way a control is: never by a GET, and never from another origin or a non-loopback host.
func TestListingAnAccountIsAControlNotARead(t *testing.T) {
	srv, _, calls := listingServer(t, func(string) ([]ghauth.Repository, error) { return nil, nil })

	if rec := get(t, srv, "/api/accounts/has-ghas/repositories"); rec.Code == http.StatusOK {
		t.Errorf("GET answered %d", rec.Code)
	}
	if rec := post(t, srv, "/api/accounts/has-ghas/repositories"); rec.Code != http.StatusForbidden {
		t.Errorf("a POST without the control header answered %d, want 403", rec.Code)
	}
	if len(*calls) != 0 {
		t.Errorf("gh was asked %+v by requests that should have been refused", *calls)
	}
}

// The name becomes part of a gh path; refusing it here keeps a malformed one from reaching gh at
// all, and says why rather than failing inside it.
func TestANameThatIsNotAnAccountIsRefused(t *testing.T) {
	srv, _, calls := listingServer(t, func(string) ([]ghauth.Repository, error) { return nil, nil })

	rec, _ := listRepositories(t, srv, "-not-a-name")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Code)
	}
	if len(*calls) != 0 {
		t.Errorf("gh was asked %+v", *calls)
	}
}
