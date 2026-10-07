package catalog

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/work"
)

const (
	reviewBase = "2e5c3a7bd6fde0d96ab38c9c22ad5c16068c883c"
	reviewHead = "085d022ea3786908bc0d9a06c4252f349d572105"
)

// What GitHub answered for has-ghas/test-repo-3 pinning a composite action at its v1 tag, with an
// npm change beside it: one workflow's `uses:` moved from a branch to a tag.
const pinnedComparison = `[
 {"change_type":"added","manifest":".github/workflows/ci.yml","ecosystem":"actions",
  "name":"has-ghas/test-repo-1","version":"1.*.*",
  "package_url":"pkg:githubactions/has-ghas/test-repo-1@1.%2A.%2A","license":null,
  "source_repository_url":null,"scope":"runtime","vulnerabilities":[]},
 {"change_type":"removed","manifest":".github/workflows/ci.yml","ecosystem":"actions",
  "name":"has-ghas/test-repo-1","version":"main","package_url":"pkg:githubactions/has-ghas/test-repo-1",
  "license":null,"source_repository_url":null,"scope":"runtime","vulnerabilities":[]},
 {"change_type":"added","manifest":"package-lock.json","ecosystem":"npm","name":"left-pad",
  "version":"1.3.0","package_url":"pkg:npm/left-pad@1.3.0","license":"WTFPL",
  "source_repository_url":null,"scope":"runtime","vulnerabilities":[]}]`

func comparisonServer(t *testing.T, handler http.HandlerFunc) (*Client, *int) {
	t.Helper()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "token", srv.Client()), &calls
}

// Dependency review names what a workflow's `uses:` gained and lost between two commits, which is
// what says a push changed the graph's shape. Every other ecosystem is somebody else's question.
func TestADependencyComparisonNamesEachActionsDependencyThatMoved(t *testing.T) {
	var asked string
	c, calls := comparisonServer(t, func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		w.Header().Set("X-GitHub-Dependency-Graph-Snapshot-Warnings", "")
		fmt.Fprint(w, pinnedComparison)
	})

	got, err := c.CompareDependencies(context.Background(), "has-ghas/test-repo-3", reviewBase, reviewHead)
	if err != nil {
		t.Fatalf("CompareDependencies: %v", err)
	}

	if want := "/repos/has-ghas/test-repo-3/dependency-graph/compare/" + reviewBase + "..." + reviewHead; asked != want {
		t.Errorf("asked %q, want %q", asked, want)
	}
	if *calls != 1 || len(got.Changes) != 3 || got.Warnings != "" {
		t.Errorf("%d requests, %d changes, warnings %q; want one request and all three changes",
			*calls, len(got.Changes), got.Warnings)
	}
	actions := got.Actions()
	if len(actions) != 2 {
		t.Fatalf("actions = %+v, want the two workflow changes and not the npm one", actions)
	}
	if a := actions[0]; a.ChangeType != "added" || a.Name != "has-ghas/test-repo-1" || a.Version != "1.*.*" ||
		a.Manifest != ".github/workflows/ci.yml" {
		t.Errorf("first change = %+v, want test-repo-1 added at 1.*.* in ci.yml", a)
	}
}

// GitHub says when it could not compare everything, such as a snapshot missing for one commit, in a
// header the official action reads as base64. An incomplete comparison must say so.
func TestAnIncompleteComparisonSaysWhatGitHubSaid(t *testing.T) {
	note := "No snapshots were found for the head SHA"
	c, _ := comparisonServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-GitHub-Dependency-Graph-Snapshot-Warnings",
			base64.StdEncoding.EncodeToString([]byte(note)))
		fmt.Fprint(w, `[]`)
	})

	got, err := c.CompareDependencies(context.Background(), "octo/app", reviewBase, reviewHead)
	if err != nil {
		t.Fatalf("CompareDependencies: %v", err)
	}
	if got.Warnings != note {
		t.Errorf("warnings = %q, want %q", got.Warnings, note)
	}
}

// A comparison is asked about once per push, so a cached copy is never read again and the cache
// would keep one row per push forever.
func TestAComparisonIsNeverKeptInTheResponseCache(t *testing.T) {
	var conditional bool
	c, calls := comparisonServer(t, func(w http.ResponseWriter, r *http.Request) {
		conditional = conditional || r.Header.Get("If-None-Match") != ""
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, `[]`)
	})
	cache := newFakeCache()
	c.Cache = cache

	for range 2 {
		if _, err := c.CompareDependencies(context.Background(), "octo/app", reviewBase, reviewHead); err != nil {
			t.Fatalf("CompareDependencies: %v", err)
		}
	}
	if *calls != 2 || cache.saves != 0 || cache.lookups != 0 || conditional {
		t.Errorf("%d requests, saves %d, lookups %d, conditional %v; want both asked and the cache untouched",
			*calls, cache.saves, cache.lookups, conditional)
	}
}

// A private repository without Advanced Security, or a fork, is refused; the refusal is an answer
// the caller reports, never an empty comparison.
func TestARefusedComparisonIsNotAnEmptyOne(t *testing.T) {
	c, _ := comparisonServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Dependency review is not supported on this repository."}`)
	})

	_, err := c.CompareDependencies(context.Background(), "octo/private", reviewBase, reviewHead)
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusForbidden {
		t.Errorf("err = %v, want GitHub's 403", err)
	}
}

// Nothing is asked without both commits: an empty one would compare against whatever GitHub
// resolves it to.
func TestAComparisonNeedsBothCommits(t *testing.T) {
	c, calls := comparisonServer(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `[]`) })

	for _, pair := range [][2]string{{"", reviewHead}, {reviewBase, ""}} {
		if _, err := c.CompareDependencies(context.Background(), "octo/app", pair[0], pair[1]); err == nil {
			t.Errorf("compared %q...%q", pair[0], pair[1])
		}
	}
	if *calls != 0 {
		t.Errorf("%d requests, want none", *calls)
	}
}

// GitHub answers every change on one page today. A Link to another page is followed, because an
// Actions change listed after a page of other ecosystems is still a change.
func TestAComparisonOnSeveralPagesIsReadWhole(t *testing.T) {
	var srvURL string
	c, calls := comparisonServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `[{"change_type":"added","manifest":".github/workflows/ci.yml","ecosystem":"actions",
			  "name":"actions/setup-go","version":"6.*.*"}]`)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=2>; rel="next"`, srvURL, r.URL.Path))
		fmt.Fprint(w, `[{"change_type":"added","manifest":"package-lock.json","ecosystem":"npm",
		  "name":"left-pad","version":"1.3.0"}]`)
	})
	srvURL = c.baseURL

	got, err := c.CompareDependencies(context.Background(), "octo/app", reviewBase, reviewHead)
	if err != nil {
		t.Fatalf("CompareDependencies: %v", err)
	}
	if *calls != 2 || len(got.Actions()) != 1 || got.Actions()[0].Name != "actions/setup-go" {
		t.Errorf("%d requests, actions %+v; want both pages read and setup-go found", *calls, got.Actions())
	}
}

// What a comparison costs is what the pool its client draws on records: one request a page.
func TestAComparisonIsOneRequestOnItsPool(t *testing.T) {
	c, _ := comparisonServer(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `[]`) })
	pools := pooled(c)

	if _, err := c.CompareDependencies(context.Background(), "octo/app", reviewBase, reviewHead); err != nil {
		t.Fatalf("CompareDependencies: %v", err)
	}
	if requests, charged := spentBy(pools, work.UnitREST); requests != 1 || charged != 1 {
		t.Errorf("%d requests charged %d, want one of each", requests, charged)
	}
}
