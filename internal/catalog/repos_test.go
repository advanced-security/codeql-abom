package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListRepositoriesPaginates(t *testing.T) {
	var page2URL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, page2URL))
			fmt.Fprint(w, `[{"full_name":"octo-org/a","archived":false},
			                {"full_name":"octo-org/b","archived":false}]`)
		default:
			fmt.Fprint(w, `[{"full_name":"octo-org/c","archived":false}]`)
		}
	}))
	defer srv.Close()
	page2URL = srv.URL + "/organizations/1/repos?page=2"

	c := New(srv.URL, "t", srv.Client())
	c.sleep = func(time.Duration) {}

	repos, err := c.ListRepositories(context.Background(), "octo-org")
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	want := []string{"octo-org/a", "octo-org/b", "octo-org/c"}
	if strings.Join(repos, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", repos, want)
	}
}

// Archived repositories cannot receive a version bump, so including them inflates every
// remediation estimate.
func TestListRepositoriesSkipsArchived(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"full_name":"octo-org/a","archived":false},
		                {"full_name":"octo-org/old","archived":true}]`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	repos, err := c.ListRepositories(context.Background(), "octo-org")
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(repos) != 1 || repos[0] != "octo-org/a" {
		t.Errorf("got %v, want [octo-org/a]", repos)
	}
}

func TestListRepositoriesStopsWithoutNextLink(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `[{"full_name":"octo-org/a"}]`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	if _, err := c.ListRepositories(context.Background(), "octo-org"); err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if calls != 1 {
		t.Errorf("made %d calls, want 1", calls)
	}
}

func TestParseNextLink(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{`<https://api.github.com/x?page=2>; rel="next", <https://api.github.com/x?page=9>; rel="last"`,
			"https://api.github.com/x?page=2"},
		{`<https://api.github.com/x?page=1>; rel="prev"`, ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := nextLink(tt.header); got != tt.want {
			t.Errorf("nextLink(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}
