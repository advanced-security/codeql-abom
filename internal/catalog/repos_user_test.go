package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A declaration names accounts, and an account is an organization or a person. `/orgs/{name}/repos`
// answers 404 for a user, so enumerating a declared personal account against it finds nothing and
// reports it as an error rather than as the 26 repositories that are there.
func TestAUserAccountIsEnumeratedThroughTheUserEndpoint(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.URL.Path == "/orgs/data-douser/repos" {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"full_name":"data-douser/one","archived":false}]`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", nil).ListRepositories(context.Background(), "data-douser")
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}

	if len(got) != 1 || got[0].FullName != "data-douser/one" {
		t.Errorf("got %v, want the user's repositories", got)
	}
}

// An organization must not be enumerated through the user endpoint: GitHub answers it, with the
// wrong list, which is worse than an error.
func TestAnOrganizationIsStillEnumeratedThroughTheOrgEndpoint(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"full_name":"advanced-security/one","archived":false}]`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "token", nil).ListRepositories(
		context.Background(), "advanced-security"); err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}

	if len(asked) == 0 || asked[0] != "/orgs/advanced-security/repos" {
		t.Errorf("asked %v, want the organization endpoint first", asked)
	}
}

// Archived repositories are excluded from both paths, for the reason the org path already gives:
// nobody can act on one.
func TestAnArchivedRepositoryIsExcludedFromAUserAccountToo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/orgs/data-douser/repos" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"full_name":"data-douser/live","archived":false},
		                 {"full_name":"data-douser/old","archived":true}]`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", nil).ListRepositories(context.Background(), "data-douser")
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}

	for _, r := range got {
		if r.FullName == "data-douser/old" {
			t.Error("an archived repository was enumerated")
		}
	}
}
