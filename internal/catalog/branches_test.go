package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The default branch rides along in the listing ABOM already fetches, so knowing it costs no
// extra request. GitHub's own features are scoped to it — campaigns report on the default branch
// only, and Dependabot reports against it — so a tool reasoning about them has to know its name.
func TestTheDefaultBranchComesFromTheListingAlreadyFetched(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"full_name":"octo/app","archived":false,"default_branch":"trunk"}]`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", nil).ListRepositories(context.Background(), "octo")
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}

	if len(got) != 1 || got[0].FullName != "octo/app" {
		t.Fatalf("got %v, want one repository", got)
	}
	if got[0].DefaultBranch != "trunk" {
		t.Errorf("DefaultBranch = %q, want %q", got[0].DefaultBranch, "trunk")
	}
	if calls != 1 {
		t.Errorf("made %d requests, want the one listing that was already being fetched", calls)
	}
}

// A listing that names no default branch must not be reported as one. GitHub omits the field for
// an empty repository, and inventing "main" would state a fact nobody observed.
func TestARepositoryWithNoDefaultBranchReportsNone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"full_name":"octo/empty","archived":false}]`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", nil).ListRepositories(context.Background(), "octo")
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(got) != 1 || got[0].DefaultBranch != "" {
		t.Errorf("got %v, want the default branch left unstated", got)
	}
}

// Branch protection decides whether a gate can block a merge at all or only comment, so the
// question is asked server-side rather than by listing every branch and filtering.
func TestProtectedBranchesAreAskedForByName(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"main","protected":true},{"name":"release","protected":true}]`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", nil).ProtectedBranches(context.Background(), "octo/app")
	if err != nil {
		t.Fatalf("ProtectedBranches: %v", err)
	}

	if len(got) != 2 || got[0] != "main" || got[1] != "release" {
		t.Errorf("got %v, want both protected branches", got)
	}
	if !strings.Contains(asked, "protected=true") {
		t.Errorf("asked %q, want the filter applied server-side", asked)
	}
}

// A repository with no protected branches answers with an empty list, and that is a finding.
// It must not be reported the same way as a repository nobody asked about.
func TestARepositoryWithNoProtectedBranchesAnswersEmptyRatherThanFailing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", nil).ProtectedBranches(context.Background(), "octo/app")
	if err != nil {
		t.Fatalf("ProtectedBranches: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

// names reduces a listing to full names, for tests that only care about which repositories came
// back rather than what else the listing carried.
func names(repos []Repository) []string {
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		out = append(out, r.FullName)
	}
	return out
}
