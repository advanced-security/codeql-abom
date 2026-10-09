package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A commit's files are read in one query, each named by path at that commit, and a file GitHub did
// not give as text is unread rather than empty: an empty workflow declares nothing, and an unread
// one says nothing either way.
func TestACommitsFilesAreReadInOneQueryAndOneWithoutTextIsUnread(t *testing.T) {
	var asked map[string]any
	var queries int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries++
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		asked = body.Variables
		fmt.Fprint(w, `{"data":{"rateLimit":{"cost":1,"remaining":4990,"limit":5000,
		  "resetAt":"2026-10-04T13:00:00Z"},
		  "r":{"f0":{"__typename":"Blob","text":"on: push\n","isBinary":false,"isTruncated":false},
		       "f1":{"__typename":"Blob","text":null,"isBinary":true,"isTruncated":false},
		       "f2":{"__typename":"Blob","text":"jobs:","isBinary":false,"isTruncated":true},
		       "f3":null}}}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "t", srv.Client())

	paths := []string{".github/workflows/ci.yml", "logo.yml", "big/action.yml", "gone.yml"}
	got, err := c.CommitFiles(context.Background(), "octo/app", "a1", paths)
	if err != nil {
		t.Fatal(err)
	}
	if queries != 1 || got.Requests != 1 || got.Cost != 1 || !got.Priced {
		t.Errorf("%d queries, batch %+v: want one priced query", queries, got)
	}
	if asked["o"] != "octo" || asked["n"] != "app" || asked["e0"] != "a1:.github/workflows/ci.yml" {
		t.Errorf("variables %v: want each path named at the commit", asked)
	}
	if f := got.Files[".github/workflows/ci.yml"]; f.Text != "on: push\n" || f.Unread != "" {
		t.Errorf("the workflow read as %+v", f)
	}
	for path, why := range map[string]string{"logo.yml": "binary", "big/action.yml": "part of it",
		"gone.yml": "no file"} {
		if f := got.Files[path]; f.Text != "" || !strings.Contains(f.Unread, why) {
			t.Errorf("%s read as %+v, want unread: %s", path, f, why)
		}
	}
}

// A repository GitHub would not answer for leaves every file unread with its reason, never empty.
func TestARepositoryGitHubWouldNotAnswerForLeavesEveryFileUnread(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":{"rateLimit":{"cost":1,"remaining":10,"limit":5000,
		  "resetAt":"2026-10-04T13:00:00Z"},"r":null},
		  "errors":[{"type":"NOT_FOUND","path":["r"],"message":"Could not resolve to a Repository"}]}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "t", srv.Client())

	got, err := c.CommitFiles(context.Background(), "octo/private", "a1", []string{"action.yml"})
	if err != nil {
		t.Fatal(err)
	}
	if f := got.Files["action.yml"]; !strings.Contains(f.Unread, "Could not resolve") {
		t.Errorf("action.yml read as %+v, want unread with GitHub's reason", f)
	}
}
