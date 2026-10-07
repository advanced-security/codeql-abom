package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// An event names one repository, and a listing of a large account is a sweep that names only what
// was pushed or created since its marks, so an archive or a property change would wait for the
// daily full listing. One repository is read alone, as a listing that names it and is never full.
func TestOneRepositoryIsReadAloneWithItsPropertyValues(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/has-ghas/test-repo-3":
			w.Write([]byte(`{"full_name":"has-ghas/test-repo-3","archived":false,"default_branch":"main",
				"created_at":"2026-10-05T12:59:53Z","pushed_at":"2026-10-05T13:25:20Z"}`))
		case "/repos/has-ghas/test-repo-3/properties/values":
			w.Write([]byte(`[{"property_name":"business_id","value":"ABOM"},
				{"property_name":"teams","value":["red","blue"]},{"property_name":"unset","value":null}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", nil).ReadRepository(context.Background(), "has-ghas/test-repo-3")
	if err != nil {
		t.Fatalf("ReadRepository: %v", err)
	}
	if got.Full || len(got.Repositories) != 1 || !got.PropertiesRead {
		t.Fatalf("listing = %+v, want one repository, properties read, and never full", got)
	}
	r := got.Repositories[0]
	if r.FullName != "has-ghas/test-repo-3" || r.DefaultBranch != "main" || r.PushedAt.IsZero() {
		t.Errorf("repository = %+v, want its name, default branch and push time", r)
	}
	want := []PropertyValue{{"business_id", "ABOM"}, {"teams", "red"}, {"teams", "blue"}}
	if !slices.Equal(r.Properties, want) {
		t.Errorf("properties = %v, want %v, one row per value and nothing for an unset one", r.Properties, want)
	}
	if !slices.Equal(asked, []string{"/repos/has-ghas/test-repo-3", "/repos/has-ghas/test-repo-3/properties/values"}) {
		t.Errorf("asked %v, want the repository and its values and nothing else", asked)
	}
}

// No listing names an archived repository, so a single read does not either.
func TestAnArchivedRepositoryIsNotNamed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"full_name":"has-ghas/old","archived":true,"default_branch":"main"}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", nil).ReadRepository(context.Background(), "has-ghas/old")
	if err != nil || len(got.Repositories) != 0 || got.Full {
		t.Errorf("ReadRepository = %+v, %v; want nothing named and no error", got, err)
	}
}

// Values that cannot be read leave the repository named and say why, as a listing does.
func TestUnreadablePropertyValuesAreSaidNotGuessed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/repos/has-ghas/x/properties/values" {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
			return
		}
		w.Write([]byte(`{"full_name":"has-ghas/x","default_branch":"main"}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", nil).ReadRepository(context.Background(), "has-ghas/x")
	if err != nil || len(got.Repositories) != 1 || got.PropertiesRead || got.PropertiesUnread == "" {
		t.Errorf("ReadRepository = %+v, %v; want the repository named and the values unread with a reason", got, err)
	}
}

// The name comes from a webhook payload, so anything that is not a repository name asks nothing.
func TestANameThatIsNotARepositoryAsksNothing(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()

	for _, name := range []string{"", "has-ghas", "has-ghas/", "/x", "a/b/c", "has-ghas/..", "has-ghas/a?b",
		"-bad/x", "has ghas/x"} {
		if _, err := New(srv.URL, "token", nil).ReadRepository(context.Background(), name); err == nil {
			t.Errorf("ReadRepository(%q) succeeded", name)
		}
	}
	if calls != 0 {
		t.Errorf("made %d requests for names that are not repositories", calls)
	}
}
